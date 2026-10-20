// Package cli implements the wip command line.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"go.guillerg.dev/wip/internal/store"
)

const usage = `usage: wip <command> [args]

Reading
  ls [--scope S] [--repo R] [--state S,...|all] [--waiting me|others|WHO] [--archived]
                           open items by default (all with --archived), most
                           recently updated first
  show ID
  here                     open items bound to the current directory
  report [--since WHEN] [--scope S] [--repo R]
                           log entries since WHEN (default mon), grouped by item
  lint                     stale items, missing paths, unparseable files

Writing
  new TITLE --scope S [--id ID] [--here|--here-repo] [--repo R]... [--path P]...
          [--issue URL] [--link URL]... [--doc PATH] [--status TEXT]
  log ID TEXT...           append a log entry (resumes a waiting item)
  status ID TEXT...        set the short status (resumes a waiting item)
  wait ID WHO REASON...    waiting on WHO ("me" for you); REASON becomes the status
  resume ID                back to active, also reopens done/dropped
  done ID [TEXT...]
  drop ID [TEXT...]
  block ID TEXT...
  unblock ID [N]           remove blocker N (1-based), or all of them
  set ID [--title T] [--scope S] [--here|--here-repo] [--repo R] [--rm-repo R] [--path P]
         [--rm-path P] [--issue URL] [--link URL] [--rm-link URL] [--doc PATH]
  edit ID                  open in $EDITOR, validated before saving
  prune [--older AGE] [--dry-run]
                           archive done/dropped items untouched for AGE (default 30d)
  completion zsh           print the zsh completion script
  version

--here binds to the current git worktree; --here-repo to the whole repo, so the
item shows in every worktree. Both also record the repo name.
WHEN is YYYY-MM-DD, today, yesterday, a weekday (mon..sun, most recent) or an
age like 3d or 2w. Items live in $WIP_DIR (default ~/.local/share/wip).
Use -- before text that starts with a dash: wip log ID -- -1 regression.
`

// errQuiet signals a failing exit status with nothing more to print.
var errQuiet = errors.New("")

type App struct {
	Store   *store.Store
	Stdout  io.Writer
	Stderr  io.Writer
	Getwd   func() (string, error)
	Color   palette
	Version string
}

func Main(version string) int {
	s, err := store.Open(store.DefaultDir())
	if err != nil {
		fmt.Fprintln(os.Stderr, "wip:", err)
		return 1
	}
	a := &App{Store: s, Stdout: os.Stdout, Stderr: os.Stderr, Getwd: os.Getwd, Color: colorFor(os.Stdout), Version: version}
	if err := a.Run(os.Args[1:]); err != nil {
		if err != errQuiet {
			fmt.Fprintln(os.Stderr, "wip:", err)
		}
		return 1
	}
	return 0
}

func (a *App) Run(args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	cmds := map[string]func([]string) error{
		"ls": a.ls, "show": a.show, "here": a.here, "report": a.report, "lint": a.lint,
		"new": a.new, "log": a.log, "status": a.status, "wait": a.wait, "resume": a.resume,
		"done": a.finisher("done", store.Done), "drop": a.finisher("drop", store.Dropped),
		"block": a.block, "unblock": a.unblock, "set": a.set, "edit": a.edit, "prune": a.prune,
		"completion": a.completion, "_ids": a.ids, "_scopes": a.scopes,
	}
	name, rest := args[0], args[1:]
	if name == "help" || name == "-h" || name == "--help" {
		fmt.Fprint(a.Stdout, usage)
		return nil
	}
	if name == "version" || name == "--version" {
		fmt.Fprintln(a.Stdout, a.Version)
		return nil
	}
	cmd, ok := cmds[name]
	if !ok {
		return fmt.Errorf("unknown command %q; see wip help", name)
	}
	return cmd(rest)
}

func (a *App) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parse allows flags anywhere among positional args, up to a "--".
func parse(fs *flag.FlagSet, args []string, minPos, maxPos int) ([]string, error) {
	var tail []string
	if i := slices.Index(args, "--"); i >= 0 {
		args, tail = args[:i], args[i+1:]
	}
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, fmt.Errorf("%s: %w; see wip help", fs.Name(), err)
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		pos, args = append(pos, args[0]), args[1:]
	}
	pos = append(pos, tail...)
	if len(pos) < minPos || (maxPos >= 0 && len(pos) > maxPos) {
		return nil, fmt.Errorf("%s: wrong number of arguments; see wip help", fs.Name())
	}
	return pos, nil
}

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

func addUnique(xs []string, vs ...string) []string {
	for _, v := range vs {
		if !slices.Contains(xs, v) {
			xs = append(xs, v)
		}
	}
	return xs
}

func remove(xs []string, vs ...string) []string {
	return slices.DeleteFunc(xs, func(x string) bool { return slices.Contains(vs, x) })
}

// absPath resolves p against the working directory and abbreviates $HOME.
func (a *App) absPath(p string) (string, error) {
	p = store.ExpandHome(p)
	if !filepath.IsAbs(p) {
		wd, err := a.Getwd()
		if err != nil {
			return "", err
		}
		p = filepath.Join(wd, p)
	}
	return store.ContractHome(filepath.Clean(p)), nil
}

func (a *App) absPaths(ps []string) ([]string, error) {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		abs, err := a.absPath(p)
		if err != nil {
			return nil, err
		}
		out = append(out, abs)
	}
	return out, nil
}

// gitDir returns the directory to bind for the working directory: its git
// worktree or, with wholeRepo, the repo root that holds every worktree. name
// comes from the origin URL, or the root's directory name without one.
func (a *App) gitDir(wholeRepo bool) (dir, name string, err error) {
	wd, err := a.Getwd()
	if err != nil {
		return "", "", err
	}
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", wd}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	common, err := git("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", fmt.Errorf("%s is not in a git repo", wd)
	}
	root := common
	if filepath.Base(common) == ".git" {
		root = filepath.Dir(common)
	}
	dir = root
	if !wholeRepo {
		if dir, err = git("rev-parse", "--show-toplevel"); err != nil {
			return "", "", fmt.Errorf("--here: %s is not in a git worktree; --here-repo binds the whole repo", wd)
		}
	}
	name = repoName(root)
	if url, err := git("remote", "get-url", "origin"); err == nil {
		name = repoName(url)
	}
	return store.ContractHome(dir), name, nil
}

// repoName is the last segment of a remote URL, slug or path, without .git:
// git@github.com:o/r.git, https://github.com/o/r and o/r all give r.
func repoName(s string) string {
	s = strings.TrimSuffix(strings.TrimRight(s, "/"), ".git")
	return s[strings.LastIndexAny(s, "/:")+1:]
}

func repoNames(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = repoName(s)
	}
	return out
}
