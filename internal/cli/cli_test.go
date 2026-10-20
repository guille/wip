package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"go.guillerg.dev/wip/internal/store"
)

type harness struct {
	t   *testing.T
	app *App
	out *bytes.Buffer
	now time.Time
	wd  string
}

func newHarness(t *testing.T) *harness {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, out: &bytes.Buffer{}, now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.Local), wd: "/w/repo"}
	s.Now = func() time.Time { return h.now }
	h.app = &App{Store: s, Stdout: h.out, Stderr: h.out, Getwd: func() (string, error) { return h.wd, nil }}
	return h
}

func (h *harness) run(args ...string) string {
	h.t.Helper()
	h.out.Reset()
	if err := h.app.Run(args); err != nil {
		h.t.Fatalf("wip %s: %v", strings.Join(args, " "), err)
	}
	return h.out.String()
}

func (h *harness) fail(args ...string) {
	h.t.Helper()
	if err := h.app.Run(args); err == nil {
		h.t.Fatalf("wip %s: succeeded", strings.Join(args, " "))
	}
}

func TestWaitingLifecycle(t *testing.T) {
	h := newHarness(t)
	h.run("new", "Fix", "the", "thing", "--scope", "work", "--path", ".")
	h.run("wait", "fix-the-thing", "me", "pick", "A", "or", "B")

	if got := h.run("ls", "--waiting", "me"); !strings.Contains(got, "fix-the-thing") {
		t.Errorf("ls --waiting me: %q", got)
	}
	if got := h.run("ls", "--waiting", "others"); got != "" {
		t.Errorf("ls --waiting others: %q", got)
	}
	if got := h.run("here"); !strings.Contains(got, "state: waiting on me") {
		t.Errorf("here: %q", got)
	}

	h.run("log", "fix-the-thing", "went with A")
	it, _ := h.app.Store.Get("fix-the-thing")
	if it.State != store.Active || it.WaitingOn != "" {
		t.Errorf("log should resume: %s %q", it.State, it.WaitingOn)
	}

	h.run("done", "fix-the-thing")
	h.fail("wait", "fix-the-thing", "bob", "review")
	h.run("resume", "fix-the-thing")
	it, _ = h.app.Store.Get("fix-the-thing")
	if it.State != store.Active || it.Log[0].Text != "reopened" {
		t.Errorf("resume should reopen: %+v", it)
	}
}

func TestHereIgnoresOtherDirs(t *testing.T) {
	h := newHarness(t)
	h.run("new", "Thing", "--scope", "work", "--path", "/w/repo/wt-a")
	h.wd = "/w/repo/wt-b"
	if got := h.run("here"); got != "" {
		t.Errorf("here in unrelated worktree: %q", got)
	}
	h.wd = "/w/repo/wt-a/sub"
	if got := h.run("here"); !strings.Contains(got, "## thing: Thing") {
		t.Errorf("here in bound worktree: %q", got)
	}
}

func TestReportAndPrune(t *testing.T) {
	h := newHarness(t)
	h.now = time.Date(2026, 9, 1, 12, 0, 0, 0, time.Local)
	h.run("new", "Old", "--scope", "work")
	h.run("done", "old", "shipped")
	h.now = time.Date(2026, 10, 13, 12, 0, 0, 0, time.Local) // a Tuesday
	h.run("new", "New", "--scope", "personal")
	h.run("log", "new", "started")

	if got := h.run("report", "--since", "mon"); strings.Contains(got, "old") || !strings.Contains(got, "2026-10-13 started") {
		t.Errorf("report --since mon: %q", got)
	}
	if got := h.run("report", "--since", "2026-08-01", "--scope", "work"); !strings.Contains(got, "done: shipped") || strings.Contains(got, "started") {
		t.Errorf("report --scope work: %q", got)
	}

	if got := h.run("prune", "--dry-run"); !strings.Contains(got, "old") {
		t.Errorf("prune --dry-run: %q", got)
	}
	h.run("prune")
	if got := h.run("ls", "--state", "all"); strings.Contains(got, "old") {
		t.Errorf("pruned item still listed: %q", got)
	}
	if got := h.run("report", "--since", "2026-08-01"); !strings.Contains(got, "shipped") {
		t.Errorf("report should read the archive: %q", got)
	}
	if got := h.run("ls", "--archived"); !strings.Contains(got, "old") {
		t.Errorf("ls --archived: %q", got)
	}
	if err := h.app.Store.Archive("new"); err == nil {
		t.Error("archived an open item")
	}

	h.run("resume", "old")
	it, err := h.app.Store.Get("old")
	if err != nil || it.Archived || it.State != store.Active {
		t.Errorf("resume should unarchive: %+v %v", it, err)
	}
}

func TestRejects(t *testing.T) {
	h := newHarness(t)
	h.fail("ls", "--state", "activ")
	h.fail("new", "日本", "--scope", "work")
	h.fail("new", "T", "--scope", "work", "--link", "https://a\nb")
}

func TestSetLists(t *testing.T) {
	h := newHarness(t)
	h.run("new", "T", "--scope", "work", "--repo", "o/a")
	h.run("set", "t", "--repo", "o/b", "--rm-repo", "o/a", "--doc", "notes.md", "--title", "Renamed")
	it, _ := h.app.Store.Get("t")
	if strings.Join(it.Repos, ",") != "b" || it.Doc != "/w/repo/notes.md" || it.Title != "Renamed" {
		t.Errorf("set: %+v", it)
	}
	h.run("set", "t", "--doc", "")
	if it, _ := h.app.Store.Get("t"); it.Doc != "" {
		t.Errorf("--doc '' should clear: %q", it.Doc)
	}
	h.fail("set", "t")
}

func TestParseWhen(t *testing.T) {
	tue := time.Date(2026, 10, 13, 9, 0, 0, 0, time.Local)
	for in, want := range map[string]string{
		"today": "2026-10-13", "yesterday": "2026-10-12", "mon": "2026-10-12",
		"Tuesday": "2026-10-13", "wed": "2026-10-07", "3d": "2026-10-10", "2w": "2026-09-29",
		"2026-01-02": "2026-01-02",
	} {
		got, err := parseWhen(in, tue)
		if err != nil || got.String() != want {
			t.Errorf("parseWhen(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	if _, err := parseWhen("soon", tue); err == nil {
		t.Error("parseWhen(soon) accepted")
	}
}

func TestSlugAndRepoName(t *testing.T) {
	if got := slugify("Speed up api's reconcile loop, finally (for real this time)"); got != "speed-up-api-s-reconcile-loop-finally" {
		t.Errorf("slugify: %q", got)
	}
	for in, want := range map[string]string{
		"git@github.com:acme/api.git":      "api",
		"https://github.com/acme/wip/":     "wip",
		"ssh://git@host:22/group/sub/repo": "repo",
		"acme/api":                         "api",
		"api":                              "api",
		"/home/me/src/api":                 "api",
		"/srv/git/api.git":                 "api",
	} {
		if got := repoName(in); got != want {
			t.Errorf("repoName(%q) = %q", in, got)
		}
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestHereBinding(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	src := t.TempDir()
	git(t, src, "init", "-q", "-b", "main")
	git(t, src, "commit", "-q", "--allow-empty", "-m", "init")

	// A bare repo in repo/.git with worktrees beside it.
	repo := filepath.Join(t.TempDir(), "api")
	git(t, src, "clone", "-q", "--bare", src, filepath.Join(repo, ".git"))
	git(t, repo, "--git-dir=.git", "remote", "set-url", "origin", "git@github.com:acme/api.git")
	git(t, repo, "--git-dir=.git", "worktree", "add", "-q", filepath.Join(repo, "feat", "a"), "-b", "feat/a")
	wt := filepath.Join(repo, "feat", "a")

	h := newHarness(t)
	h.wd = filepath.Join(wt, "sub")
	if err := os.MkdirAll(h.wd, 0o755); err != nil {
		t.Fatal(err)
	}
	h.run("new", "Branch", "--scope", "work", "--here")
	h.run("new", "Planning", "--scope", "work", "--here-repo", "--repo", "acme/api")
	b, _ := h.app.Store.Get("branch")
	p, _ := h.app.Store.Get("planning")
	if strings.Join(b.Paths, ",") != store.ContractHome(wt) || strings.Join(b.Repos, ",") != "api" {
		t.Errorf("--here: %v %v", b.Paths, b.Repos)
	}
	if strings.Join(p.Paths, ",") != store.ContractHome(repo) || strings.Join(p.Repos, ",") != "api" {
		t.Errorf("--here-repo: %v %v", p.Paths, p.Repos)
	}

	h.wd = repo // the bare root: whole-repo binding works, worktree binding can't
	if got := h.run("here"); !strings.Contains(got, "planning") || strings.Contains(got, "## branch") {
		t.Errorf("here at repo root: %q", got)
	}
	h.fail("set", "planning", "--here")
	h.run("set", "branch", "--here-repo")

	// A plain checkout is its own repo root.
	h.wd = src
	h.run("new", "Plain", "--scope", "personal", "--here", "--here-repo")
	if it, _ := h.app.Store.Get("plain"); strings.Join(it.Paths, ",") != store.ContractHome(src) || it.Repos[0] != filepath.Base(src) {
		t.Errorf("plain checkout: %v %v", it.Paths, it.Repos)
	}
}

func TestColor(t *testing.T) {
	h := newHarness(t)
	h.run("new", "A", "--scope", "work")
	h.run("new", "Longer one", "--scope", "personal")
	h.run("wait", "a", "me", "decide")
	h.run("block", "longer-one", "x")
	plain := h.run("ls")
	if strings.Contains(plain, "\x1b") {
		t.Fatalf("escape codes without color: %q", plain)
	}

	h.app.Color = palette{on: true}
	colored := h.run("ls")
	if !strings.Contains(colored, "\x1b") {
		t.Fatalf("no escape codes with color: %q", colored)
	}
	if got := ansi.Strip(colored); got != plain {
		t.Errorf("colored output differs once stripped:\n%s\nvs\n%s", got, plain)
	}
}

func TestIDs(t *testing.T) {
	h := newHarness(t)
	h.run("new", "Open", "--scope", "work")
	h.run("new", "Closed", "--scope", "work")
	h.run("done", "closed")
	if got := h.run("_ids"); got != "open\tactive\tOpen\n" {
		t.Errorf("_ids: %q", got)
	}
	if got := h.run("_ids", "--all"); !strings.Contains(got, "closed\tdone\tClosed") {
		t.Errorf("_ids --all: %q", got)
	}
}

func TestScopes(t *testing.T) {
	h := newHarness(t)
	h.run("new", "A", "--scope", "work")
	h.run("new", "B", "--scope", "personal")
	h.run("new", "C", "--scope", "work")
	h.run("done", "c")
	if got := h.run("_scopes"); got != "personal\nwork\n" {
		t.Errorf("_scopes: %q", got)
	}
}
