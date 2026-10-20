package cli

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.guillerg.dev/wip/internal/store"
)

func (a *App) new(args []string) error {
	fs := a.flags("new")
	id := fs.String("id", "", "")
	scope := fs.String("scope", "", "")
	here := fs.Bool("here", false, "")
	hereRepo := fs.Bool("here-repo", false, "")
	issue := fs.String("issue", "", "")
	doc := fs.String("doc", "", "")
	status := fs.String("status", "", "")
	var repos, paths, links list
	fs.Var(&repos, "repo", "")
	fs.Var(&paths, "path", "")
	fs.Var(&links, "link", "")
	pos, err := parse(fs, args, 1, -1)
	if err != nil {
		return err
	}

	it := &store.Item{
		ID:     *id,
		Title:  strings.Join(pos, " "),
		Scope:  *scope,
		State:  store.Active,
		Status: *status,
		Issue:  *issue,
		Links:  addUnique(nil, links...),
		Repos:  addUnique(nil, repoNames(repos)...),
	}
	if it.ID == "" {
		if it.ID = slugify(it.Title); it.ID == "" {
			return errors.New("new: cannot derive an ID from the title; pass --id")
		}
	}
	if it.Paths, err = a.absPaths(addUnique(nil, paths...)); err != nil {
		return err
	}
	if *doc != "" {
		if it.Doc, err = a.absPath(*doc); err != nil {
			return err
		}
	}
	if err := a.bindHere(it, *here, *hereRepo); err != nil {
		return err
	}
	if err := a.Store.Create(it); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, it.ID)
	return nil
}

// bindHere adds the current worktree and/or whole repo to the item's paths.
func (a *App) bindHere(it *store.Item, worktree, wholeRepo bool) error {
	var modes []bool
	if worktree {
		modes = append(modes, false)
	}
	if wholeRepo {
		modes = append(modes, true)
	}
	for _, whole := range modes {
		dir, name, err := a.gitDir(whole)
		if err != nil {
			return err
		}
		it.Paths = addUnique(it.Paths, dir)
		it.Repos = addUnique(it.Repos, name)
	}
	return nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slugify derives an ID from a title, cut at a word boundary.
func slugify(title string) string {
	s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
	for len(s) > 40 {
		i := strings.LastIndexByte(s, '-')
		if i <= 0 {
			s = s[:40]
			break
		}
		s = s[:i]
	}
	return s
}

// update runs a mutation on ID and reports it.
func (a *App) update(id string, fn func(*store.Item) error) error {
	it, err := a.Store.Update(id, fn)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "%s: %s\n", it.ID, stateLabel(it))
	return nil
}

func (a *App) log(args []string) error {
	pos, err := parse(a.flags("log"), args, 2, -1)
	if err != nil {
		return err
	}
	return a.update(pos[0], func(it *store.Item) error {
		it.Resume()
		it.AddLog(a.Store.Today(), strings.Join(pos[1:], " "))
		return nil
	})
}

func (a *App) status(args []string) error {
	pos, err := parse(a.flags("status"), args, 2, -1)
	if err != nil {
		return err
	}
	return a.update(pos[0], func(it *store.Item) error {
		it.Resume()
		it.Status = strings.Join(pos[1:], " ")
		return nil
	})
}

func (a *App) wait(args []string) error {
	pos, err := parse(a.flags("wait"), args, 3, -1)
	if err != nil {
		return err
	}
	who, reason := pos[1], strings.Join(pos[2:], " ")
	return a.update(pos[0], func(it *store.Item) error {
		if !it.State.Open() {
			return fmt.Errorf("%s is %s; resume it first", it.ID, it.State)
		}
		it.State, it.WaitingOn, it.Status = store.Waiting, who, reason
		it.AddLog(a.Store.Today(), fmt.Sprintf("waiting on %s: %s", who, reason))
		return nil
	})
}

func (a *App) resume(args []string) error {
	pos, err := parse(a.flags("resume"), args, 1, 1)
	if err != nil {
		return err
	}
	it, err := a.Store.Get(pos[0])
	if err != nil {
		return err
	}
	if it.Archived {
		if err := a.Store.Unarchive(it.ID); err != nil {
			return err
		}
	}
	return a.update(it.ID, func(it *store.Item) error {
		if it.State == store.Active {
			return nil
		}
		if !it.State.Open() {
			it.AddLog(a.Store.Today(), "reopened")
		}
		it.State, it.WaitingOn = store.Active, ""
		return nil
	})
}

func (a *App) finisher(name string, state store.State) func([]string) error {
	return func(args []string) error {
		pos, err := parse(a.flags(name), args, 1, -1)
		if err != nil {
			return err
		}
		entry := string(state)
		if len(pos) > 1 {
			entry += ": " + strings.Join(pos[1:], " ")
		}
		return a.update(pos[0], func(it *store.Item) error {
			it.State, it.WaitingOn = state, ""
			it.AddLog(a.Store.Today(), entry)
			return nil
		})
	}
}

func (a *App) block(args []string) error {
	pos, err := parse(a.flags("block"), args, 2, -1)
	if err != nil {
		return err
	}
	return a.update(pos[0], func(it *store.Item) error {
		it.Blockers = addUnique(it.Blockers, strings.Join(pos[1:], " "))
		return nil
	})
}

func (a *App) unblock(args []string) error {
	pos, err := parse(a.flags("unblock"), args, 1, 2)
	if err != nil {
		return err
	}
	return a.update(pos[0], func(it *store.Item) error {
		if len(pos) == 1 {
			it.Blockers = nil
			return nil
		}
		n, err := strconv.Atoi(pos[1])
		if err != nil || n < 1 || n > len(it.Blockers) {
			return fmt.Errorf("%s has no blocker %s", it.ID, pos[1])
		}
		it.Blockers = append(it.Blockers[:n-1], it.Blockers[n:]...)
		return nil
	})
}

func (a *App) set(args []string) error {
	fs := a.flags("set")
	title := fs.String("title", "", "")
	scope := fs.String("scope", "", "")
	issue := fs.String("issue", "", "")
	doc := fs.String("doc", "", "")
	here := fs.Bool("here", false, "")
	hereRepo := fs.Bool("here-repo", false, "")
	var repos, rmRepos, paths, rmPaths, links, rmLinks list
	fs.Var(&repos, "repo", "")
	fs.Var(&rmRepos, "rm-repo", "")
	fs.Var(&paths, "path", "")
	fs.Var(&rmPaths, "rm-path", "")
	fs.Var(&links, "link", "")
	fs.Var(&rmLinks, "rm-link", "")
	pos, err := parse(fs, args, 1, 1)
	if err != nil {
		return err
	}
	if fs.NFlag() == 0 {
		return errors.New("set: nothing to change; see wip help")
	}
	addPaths, err := a.absPaths(paths)
	if err != nil {
		return err
	}
	dropPaths, err := a.absPaths(rmPaths)
	if err != nil {
		return err
	}
	docPath := *doc
	if docPath != "" {
		if docPath, err = a.absPath(docPath); err != nil {
			return err
		}
	}

	return a.update(pos[0], func(it *store.Item) error {
		if flagSet(fs, "title") {
			it.Title = *title
		}
		if flagSet(fs, "scope") {
			it.Scope = *scope
		}
		if flagSet(fs, "issue") {
			it.Issue = *issue
		}
		if flagSet(fs, "doc") {
			it.Doc = docPath
		}
		it.Repos = remove(addUnique(it.Repos, repoNames(repos)...), repoNames(rmRepos)...)
		it.Paths = remove(addUnique(it.Paths, addPaths...), dropPaths...)
		it.Links = remove(addUnique(it.Links, links...), rmLinks...)
		return a.bindHere(it, *here, *hereRepo)
	})
}

func (a *App) edit(args []string) error {
	pos, err := parse(a.flags("edit"), args, 1, 1)
	if err != nil {
		return err
	}
	it, err := a.Store.Get(pos[0])
	if err != nil {
		return err
	}
	if it.Archived {
		return fmt.Errorf("%s is archived", it.ID)
	}
	before, err := os.ReadFile(a.Store.Path(it))
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "wip-"+it.ID+"-*.md")
	if err != nil {
		return err
	}
	tmp.Close()
	if err := os.WriteFile(tmp.Name(), before, 0o600); err != nil {
		return err
	}

	editor := cmp.Or(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	cmd := exec.Command("sh", "-c", editor+` "$1"`, "sh", tmp.Name())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w (edit kept at %s)", editor, err, tmp.Name())
	}
	after, err := os.ReadFile(tmp.Name())
	if err != nil {
		return err
	}
	if bytes.Equal(before, after) {
		os.Remove(tmp.Name())
		return nil
	}
	if err := a.Store.Replace(it.ID, before, after); err != nil {
		return fmt.Errorf("%w\nedit kept at %s", err, tmp.Name())
	}
	os.Remove(tmp.Name())
	return nil
}

func (a *App) prune(args []string) error {
	fs := a.flags("prune")
	older := fs.String("older", "30d", "")
	dry := fs.Bool("dry-run", false, "")
	if _, err := parse(fs, args, 0, 0); err != nil {
		return err
	}
	age, ok := parseAge(*older)
	if !ok {
		return fmt.Errorf("invalid age %q, want e.g. 30d or 4w", *older)
	}
	cutoff := store.DateOf(a.Store.Now().Add(-age))
	for _, it := range a.list(false) {
		if it.State.Open() || !it.Updated.Before(cutoff.Time) {
			continue
		}
		dst := filepath.Join("archive", it.ID+".md")
		if *dry {
			fmt.Fprintf(a.Stdout, "would archive %s\n", dst)
			continue
		}
		if err := a.Store.Archive(it.ID); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "archived %s\n", dst)
	}
	return nil
}
