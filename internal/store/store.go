package store

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

var ErrNotFound = errors.New("no such workstream")

// Store is a directory of ID.md files, with done items moved to archive/.
// Writes are serialized with a flock and land via atomic rename; nothing is
// ever deleted.
type Store struct {
	Dir string
	Now func() time.Time
}

func DefaultDir() string {
	if d := os.Getenv("WIP_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "wip")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "wip")
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "archive"), 0o755); err != nil {
		return nil, err
	}
	return &Store{Dir: dir, Now: time.Now}, nil
}

func (s *Store) Today() Date { return DateOf(s.Now()) }

func (s *Store) path(id string, archived bool) string {
	if archived {
		return filepath.Join(s.Dir, "archive", id+".md")
	}
	return filepath.Join(s.Dir, id+".md")
}

func (s *Store) Path(it *Item) string { return s.path(it.ID, it.Archived) }

// List returns items sorted by most recently updated. Unparseable files are
// reported in errs rather than failing the whole listing.
func (s *Store) List(withArchive bool) (items []*Item, errs []error) {
	dirs := []bool{false}
	if withArchive {
		dirs = append(dirs, true)
	}
	for _, archived := range dirs {
		dir := filepath.Dir(s.path("x", archived))
		ents, err := os.ReadDir(dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, e := range ents {
			id, ok := strings.CutSuffix(e.Name(), ".md")
			if !ok || e.IsDir() || strings.HasPrefix(id, ".") {
				continue
			}
			it, err := s.read(id, archived)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			items = append(items, it)
		}
	}
	slices.SortFunc(items, func(a, b *Item) int {
		if c := b.Updated.Compare(a.Updated.Time); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return items, errs
}

func (s *Store) read(id string, archived bool) (*Item, error) {
	data, err := os.ReadFile(s.path(id, archived))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	it, err := Parse(id, data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.path(id, archived), err)
	}
	it.Archived = archived
	return it, nil
}

// Get finds an item, falling back to the archive.
func (s *Store) Get(id string) (*Item, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	it, err := s.read(id, false)
	if errors.Is(err, ErrNotFound) {
		return s.read(id, true)
	}
	return it, err
}

func (s *Store) Create(it *Item) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	if _, err := s.Get(it.ID); !errors.Is(err, ErrNotFound) {
		if err == nil {
			return fmt.Errorf("%s already exists", it.ID)
		}
		return err
	}
	it.Created, it.Updated = s.Today(), s.Today()
	if err := it.Validate(); err != nil {
		return err
	}
	return s.write(s.path(it.ID, false), it.Render())
}

// Update applies fn to a live (non-archived) item and saves it.
func (s *Store) Update(id string, fn func(*Item) error) (*Item, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	it, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if it.Archived {
		return nil, fmt.Errorf("%s is archived", id)
	}
	if err := fn(it); err != nil {
		return nil, err
	}
	it.Updated = s.Today()
	if err := it.Validate(); err != nil {
		return nil, err
	}
	return it, s.write(s.path(id, false), it.Render())
}

// Replace swaps an item's file for edited content, refusing if the file
// changed since before was read.
func (s *Store) Replace(id string, before, after []byte) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	p := s.path(id, false)
	cur, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if !bytes.Equal(cur, before) {
		return fmt.Errorf("%s changed while editing", id)
	}
	it, err := Parse(id, after)
	if err != nil {
		return err
	}
	it.Updated = s.Today()
	return s.write(p, it.Render())
}

// Archive moves a done or dropped item to archive/.
func (s *Store) Archive(id string) error { return s.move(id, true) }

// Unarchive moves an item back out of archive/.
func (s *Store) Unarchive(id string) error { return s.move(id, false) }

func (s *Store) move(id string, toArchive bool) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	it, err := s.read(id, !toArchive)
	if err != nil {
		return err
	}
	if toArchive && it.State.Open() {
		return fmt.Errorf("%s is %s; only done or dropped items are archived", id, it.State)
	}
	dst := s.path(id, toArchive)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	if err := os.Rename(s.path(id, !toArchive), dst); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(dst)); err != nil {
		return err
	}
	return syncDir(filepath.Dir(s.path(id, !toArchive)))
}

func (s *Store) lock() (func(), error) {
	f, err := os.OpenFile(filepath.Join(s.Dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}

func (s *Store) write(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
