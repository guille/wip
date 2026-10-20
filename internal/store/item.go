// Package store reads and writes workstream items: one Markdown file per item,
// YAML frontmatter for structured fields, free-form notes, and a dated log.
package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

type State string

const (
	Active  State = "active"
	Waiting State = "waiting"
	Done    State = "done"
	Dropped State = "dropped"
)

var States = []State{Active, Waiting, Done, Dropped}

// Open reports whether the item still needs attention.
func (s State) Open() bool { return s == Active || s == Waiting }

// Me is the waiting_on value meaning the user.
const Me = "me"

const (
	MaxTitle   = 80
	MaxStatus  = 80
	MaxBlocker = 120
	MaxWho     = 40
	MaxEntry   = 200
)

var (
	idRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,59}$`)
	scopeRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,19}$`)
)

func ValidID(id string) bool { return idRE.MatchString(id) }

type Date struct{ time.Time }

const dateLayout = "2006-01-02"

func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{time.Date(y, m, d, 0, 0, 0, 0, time.Local)}
}

func ParseDate(s string) (Date, error) {
	t, err := time.ParseInLocation(dateLayout, s, time.Local)
	if err != nil {
		return Date{}, fmt.Errorf("invalid date %q, want YYYY-MM-DD", s)
	}
	return Date{t}, nil
}

func (d Date) String() string { return d.Format(dateLayout) }

func (d Date) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!timestamp", Value: d.String()}, nil
}

func (d *Date) UnmarshalYAML(n *yaml.Node) error {
	p, err := ParseDate(n.Value)
	if err != nil {
		return err
	}
	*d = p
	return nil
}

type Entry struct {
	Date Date
	Text string
}

type Item struct {
	ID        string   `yaml:"-"`
	Title     string   `yaml:"title"`
	Scope     string   `yaml:"scope"`
	State     State    `yaml:"state"`
	WaitingOn string   `yaml:"waiting_on,omitempty"`
	Status    string   `yaml:"status,omitempty"`
	Blockers  []string `yaml:"blockers,omitempty"`
	Repos     []string `yaml:"repos,omitempty"`
	Paths     []string `yaml:"paths,omitempty"`
	Issue     string   `yaml:"issue,omitempty"`
	Links     []string `yaml:"links,omitempty"`
	Doc       string   `yaml:"doc,omitempty"`
	Created   Date     `yaml:"created"`
	Updated   Date     `yaml:"updated"`

	Notes    string  `yaml:"-"`
	Log      []Entry `yaml:"-"` // newest first
	Archived bool    `yaml:"-"`
}

// AddLog prepends an entry, keeping the log newest first.
func (it *Item) AddLog(d Date, text string) {
	it.Log = slices.Insert(it.Log, 0, Entry{d, text})
}

// Resume moves a waiting item back to active.
func (it *Item) Resume() {
	if it.State == Waiting {
		it.State = Active
		it.WaitingOn = ""
	}
}

// Covers reports whether dir is inside one of the item's paths.
func (it *Item) Covers(dir string) bool {
	dir = filepath.Clean(dir)
	for _, p := range it.Paths {
		rel, err := filepath.Rel(ExpandHome(p), dir)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			return true
		}
	}
	return false
}

func (it *Item) HasRepo(name string) bool { return slices.Contains(it.Repos, name) }

const logHeading = "## Log"

var entryRE = regexp.MustCompile(`^- (\d{4}-\d{2}-\d{2}) (.+)$`)

func Parse(id string, data []byte) (*Item, error) {
	rest, ok := bytes.CutPrefix(data, []byte("---\n"))
	if !ok {
		return nil, errors.New("missing frontmatter")
	}
	fm, body, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		return nil, errors.New("unterminated frontmatter")
	}

	it := &Item{ID: id}
	dec := yaml.NewDecoder(bytes.NewReader(fm))
	dec.KnownFields(true)
	if err := dec.Decode(it); err != nil {
		return nil, fmt.Errorf("frontmatter: %w", err)
	}

	lines := strings.Split(string(body), "\n")
	logAt := slices.Index(lines, logHeading)
	if logAt < 0 {
		logAt = len(lines)
	}
	it.Notes = strings.TrimSpace(strings.Join(lines[:logAt], "\n"))
	for _, l := range lines[min(logAt+1, len(lines)):] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		m := entryRE.FindStringSubmatch(l)
		if m == nil {
			return nil, fmt.Errorf("invalid log entry %q, want \"- YYYY-MM-DD text\"", l)
		}
		d, err := ParseDate(m[1])
		if err != nil {
			return nil, err
		}
		it.Log = append(it.Log, Entry{d, m[2]})
	}

	if err := it.Validate(); err != nil {
		return nil, err
	}
	return it, nil
}

func (it *Item) Render() []byte {
	var b bytes.Buffer
	b.WriteString("---\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(it); err != nil {
		panic(err) // plain struct of strings; cannot fail
	}
	_ = enc.Close()
	b.WriteString("---\n\n")
	if it.Notes != "" {
		b.WriteString(it.Notes + "\n\n")
	}
	b.WriteString(logHeading + "\n\n")
	for _, e := range it.Log {
		fmt.Fprintf(&b, "- %s %s\n", e.Date, e.Text)
	}
	return b.Bytes()
}

func (it *Item) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	line := func(field, v string, maxLen int) {
		switch {
		case strings.TrimSpace(v) == "":
			bad("%s: must not be empty", field)
		case strings.ContainsAny(v, "\r\n"):
			bad("%s: must be a single line", field)
		case utf8.RuneCountInString(v) > maxLen:
			bad("%s: longer than %d characters", field, maxLen)
		}
	}

	if !ValidID(it.ID) {
		bad("id %q: want lowercase letters, digits and dashes, up to 60", it.ID)
	}
	line("title", it.Title, MaxTitle)
	if !scopeRE.MatchString(it.Scope) {
		bad("scope %q: want lowercase letters, digits and dashes, up to 20", it.Scope)
	}
	if !slices.Contains(States, it.State) {
		bad("state %q: want one of active, waiting, done, dropped", it.State)
	}
	if it.State == Waiting {
		line("waiting_on", it.WaitingOn, MaxWho)
	} else if it.WaitingOn != "" {
		bad("waiting_on: only allowed when state is waiting")
	}
	if it.Status != "" {
		line("status", it.Status, MaxStatus)
	}
	for _, b := range it.Blockers {
		line("blocker", b, MaxBlocker)
	}
	for _, r := range it.Repos {
		if r == "" || strings.ContainsAny(r, "/: \t\r\n") {
			bad("repo %q: want a plain repo name, without owner", r)
		}
	}
	path := func(field, p string) {
		if strings.ContainsAny(p, "\r\n") || !filepath.IsAbs(ExpandHome(p)) {
			bad("%s %q: want a single absolute path", field, p)
		}
	}
	url := func(field, u string) {
		if strings.ContainsAny(u, " \t\r\n") || !strings.Contains(u, "://") {
			bad("%s %q: want a URL", field, u)
		}
	}
	for _, p := range it.Paths {
		path("path", p)
	}
	if it.Issue != "" {
		url("issue", it.Issue)
	}
	for _, l := range it.Links {
		url("link", l)
	}
	if it.Doc != "" {
		path("doc", it.Doc)
	}
	if it.Created.IsZero() || it.Updated.IsZero() {
		bad("created and updated are required")
	}
	for _, e := range it.Log {
		line("log entry", e.Text, MaxEntry)
	}
	return errors.Join(errs...)
}

func ExpandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

func ContractHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rest, ok := strings.CutPrefix(p, home+"/"); ok {
			return "~/" + rest
		}
	}
	return p
}
