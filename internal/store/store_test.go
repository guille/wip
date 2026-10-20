package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Date(2026, 10, 13, 12, 0, 0, 0, time.Local) }
	return s
}

func TestRoundTrip(t *testing.T) {
	s := testStore(t)
	it := &Item{
		ID: "perf", Title: "Speed it up", Scope: "work", State: Waiting, WaitingOn: "alice",
		Status: "PR up", Blockers: []string{"staging down"}, Repos: []string{"r"},
		Paths: []string{"~/work/r/perf"}, Issue: "https://x/1", Doc: "~/work/r/perf/HANDOFF.md",
		Notes: "some notes\n\nwith paragraphs",
	}
	it.AddLog(s.Today(), "first")
	it.AddLog(s.Today(), "second")
	if err := s.Create(it); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, "perf.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "created: 2026-10-13\n") {
		t.Errorf("dates should render unquoted:\n%s", data)
	}
	got, err := s.Get("perf")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Render()) != string(data) {
		t.Errorf("render not stable:\n%s\nvs\n%s", got.Render(), data)
	}
	if got.Notes != it.Notes || len(got.Log) != 2 || got.Log[0].Text != "second" {
		t.Errorf("lost body: %+v", got)
	}
}

func TestParseRejects(t *testing.T) {
	base := "---\ntitle: T\nscope: work\nstate: active\ncreated: 2026-10-13\nupdated: 2026-10-13\n"
	for name, doc := range map[string]string{
		"unknown field":   base + "priority: high\n---\n",
		"bad scope":       strings.Replace(base, "scope: work", "scope: Side Gigs", 1) + "---\n",
		"bad state":       strings.Replace(base, "active", "blocked", 1) + "---\n",
		"waiting no who":  strings.Replace(base, "active", "waiting", 1) + "---\n",
		"who not waiting": base + "waiting_on: me\n---\n",
		"long status":     base + "status: " + strings.Repeat("x", MaxStatus+1) + "\n---\n",
		"relative path":   base + "paths: [work/x]\n---\n",
		"repo with owner": base + "repos: [acme/api]\n---\n",
		"bad log line":    base + "---\n\n## Log\n\nfree text\n",
		"no frontmatter":  "# hi\n",
	} {
		if _, err := Parse("x", []byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Parse("x", []byte(base+"---\n")); err != nil {
		t.Errorf("minimal item rejected: %v", err)
	}
}

func TestCovers(t *testing.T) {
	it := &Item{Paths: []string{"/w/api/perf"}}
	for dir, want := range map[string]bool{
		"/w/api/perf":      true,
		"/w/api/perf/sub":  true,
		"/w/api/perf2":     false,
		"/w/api":           false,
		"/w/api/perf/../x": false,
	} {
		if got := it.Covers(dir); got != want {
			t.Errorf("Covers(%q) = %v", dir, got)
		}
	}
}

func TestConcurrentUpdates(t *testing.T) {
	s := testStore(t)
	if err := s.Create(&Item{ID: "x", Title: "T", Scope: "work", State: Active}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := s.Update("x", func(it *Item) error { it.AddLog(s.Today(), "hi"); return nil }); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	it, err := s.Get("x")
	if err != nil {
		t.Fatal(err)
	}
	if len(it.Log) != 20 {
		t.Errorf("got %d entries, want 20", len(it.Log))
	}
}

func TestArchive(t *testing.T) {
	s := testStore(t)
	if err := s.Create(&Item{ID: "x", Title: "T", Scope: "work", State: Done}); err != nil {
		t.Fatal(err)
	}
	if err := s.Archive("x"); err != nil {
		t.Fatal(err)
	}
	it, err := s.Get("x")
	if err != nil || !it.Archived {
		t.Fatalf("archived item not found: %v", err)
	}
	if _, err := s.Update("x", func(*Item) error { return nil }); err == nil {
		t.Error("updated an archived item")
	}
	if items, _ := s.List(false); len(items) != 0 {
		t.Error("archived item listed")
	}
}
