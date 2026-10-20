package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.guillerg.dev/wip/internal/store"
)

const staleAfter = 14 * 24 * time.Hour

func (a *App) list(withArchive bool) []*store.Item {
	items, errs := a.Store.List(withArchive)
	for _, err := range errs {
		fmt.Fprintln(a.Stderr, "wip: skipping", err)
	}
	return items
}

func (a *App) ls(args []string) error {
	fs := a.flags("ls")
	scope := fs.String("scope", "", "")
	repo := fs.String("repo", "", "")
	states := fs.String("state", "", "")
	waiting := fs.String("waiting", "", "")
	archived := fs.Bool("archived", false, "")
	if _, err := parse(fs, args, 0, 0); err != nil {
		return err
	}

	keep := func(it *store.Item) bool { return it.State.Open() }
	switch {
	case *states == "all", *states == "" && *archived:
		keep = func(*store.Item) bool { return true }
	case *states != "":
		want := strings.Split(*states, ",")
		for _, s := range want {
			if !slices.Contains(store.States, store.State(s)) {
				return fmt.Errorf("ls: unknown state %q; want active, waiting, done, dropped or all", s)
			}
		}
		keep = func(it *store.Item) bool { return slices.Contains(want, string(it.State)) }
	}

	p := a.Color
	var rows [][]cell
	for _, it := range a.list(*archived) {
		if !keep(it) ||
			(*scope != "" && it.Scope != *scope) ||
			(*repo != "" && !it.HasRepo(repoName(*repo))) ||
			(*waiting != "" && !waitingOn(it, *waiting)) {
			continue
		}
		status := cell{it.Status, it.Status}
		if len(it.Blockers) > 0 {
			status.plain = strings.TrimSpace("[blocked] " + it.Status)
			status.out = strings.TrimSpace(p.paint(styleBlocked, "[blocked]") + " " + it.Status)
		}
		label, updated := stateLabel(it), it.Updated.String()
		rows = append(rows, []cell{
			{it.ID, p.id(it.ID)},
			{it.Scope, it.Scope},
			{label, p.state(it, label)},
			{updated, p.faint(updated)},
			status,
		})
	}
	_, err := io.WriteString(a.Stdout, table(rows))
	return err
}

func waitingOn(it *store.Item, who string) bool {
	if it.State != store.Waiting {
		return false
	}
	switch who {
	case "others":
		return it.WaitingOn != store.Me
	default:
		return it.WaitingOn == who
	}
}

func stateLabel(it *store.Item) string {
	if it.State == store.Waiting {
		return "waiting:" + it.WaitingOn
	}
	return string(it.State)
}

func (a *App) show(args []string) error {
	pos, err := parse(a.flags("show"), args, 1, 1)
	if err != nil {
		return err
	}
	it, err := a.Store.Get(pos[0])
	if err != nil {
		return err
	}
	_, err = a.Stdout.Write(it.Render())
	return err
}

// here prints context for an agent starting in the working directory, or
// nothing when no open item is bound to it. Meant for a SessionStart hook.
func (a *App) here(args []string) error {
	if _, err := parse(a.flags("here"), args, 0, 0); err != nil {
		return err
	}
	wd, err := a.Getwd()
	if err != nil {
		return err
	}
	var items []*store.Item
	for _, it := range a.list(false) {
		if it.State.Open() && it.Covers(wd) {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return nil
	}

	out, p := a.Stdout, a.Color
	fmt.Fprintln(out, "This directory belongs to these workstreams, tracked with the `wip` CLI:")
	for _, it := range items {
		fmt.Fprintf(out, "\n%s\n", p.id(fmt.Sprintf("## %s: %s", it.ID, it.Title)))
		state := string(it.State)
		if it.State == store.Waiting {
			state = "waiting on " + it.WaitingOn
		}
		fmt.Fprintf(out, "state: %s\n", p.state(it, state))
		if it.Status != "" {
			fmt.Fprintf(out, "status: %s\n", it.Status)
		}
		for _, b := range it.Blockers {
			fmt.Fprintf(out, "%s %s\n", p.paint(styleBlocked, "blocker:"), b)
		}
		if it.Doc != "" {
			fmt.Fprintf(out, "doc: %s\n", it.Doc)
		}
		if it.Issue != "" {
			fmt.Fprintf(out, "issue: %s\n", it.Issue)
		}
		for _, l := range it.Links {
			fmt.Fprintf(out, "link: %s\n", l)
		}
		if len(it.Log) > 0 {
			fmt.Fprintln(out, "recent log:")
			for _, e := range it.Log[:min(3, len(it.Log))] {
				fmt.Fprintf(out, "  %s %s\n", p.faint(e.Date.String()), e.Text)
			}
		}
	}
	return nil
}

func (a *App) report(args []string) error {
	fs := a.flags("report")
	since := fs.String("since", "mon", "")
	scope := fs.String("scope", "", "")
	repo := fs.String("repo", "", "")
	if _, err := parse(fs, args, 0, 0); err != nil {
		return err
	}
	from, err := parseWhen(*since, a.Store.Now())
	if err != nil {
		return err
	}

	type group struct {
		it      *store.Item
		entries []store.Entry // oldest first
	}
	var groups []group
	for _, it := range a.list(true) {
		if (*scope != "" && it.Scope != *scope) || (*repo != "" && !it.HasRepo(repoName(*repo))) {
			continue
		}
		var es []store.Entry
		for _, e := range it.Log {
			if !e.Date.Before(from.Time) {
				es = append(es, e)
			}
		}
		if len(es) > 0 {
			slices.Reverse(es)
			groups = append(groups, group{it, es})
		}
	}
	slices.SortStableFunc(groups, func(x, y group) int {
		return y.entries[len(y.entries)-1].Date.Compare(x.entries[len(x.entries)-1].Date.Time)
	})

	p := a.Color
	for i, g := range groups {
		if i > 0 {
			fmt.Fprintln(a.Stdout)
		}
		fmt.Fprintf(a.Stdout, "%s: %s (%s)\n", p.id(g.it.ID), g.it.Title, p.state(g.it, stateLabel(g.it)))
		for _, e := range g.entries {
			fmt.Fprintf(a.Stdout, "  %s %s\n", p.faint(e.Date.String()), e.Text)
		}
	}
	return nil
}

func (a *App) lint(args []string) error {
	if _, err := parse(a.flags("lint"), args, 0, 0); err != nil {
		return err
	}
	items, errs := a.Store.List(false)
	var findings []string
	for _, err := range errs {
		findings = append(findings, err.Error())
	}
	cutoff := a.Store.Now().Add(-staleAfter)
	for _, it := range items {
		if !it.State.Open() {
			continue
		}
		if it.Updated.Before(cutoff) {
			findings = append(findings, fmt.Sprintf("%s: %s, untouched since %s", it.ID, stateLabel(it), it.Updated))
		}
		for _, p := range it.Paths {
			if _, err := os.Stat(store.ExpandHome(p)); errors.Is(err, os.ErrNotExist) {
				findings = append(findings, fmt.Sprintf("%s: path %s no longer exists", it.ID, p))
			}
		}
		if it.Doc != "" {
			if _, err := os.Stat(store.ExpandHome(it.Doc)); errors.Is(err, os.ErrNotExist) {
				findings = append(findings, fmt.Sprintf("%s: doc %s no longer exists", it.ID, it.Doc))
			}
		}
	}
	for _, f := range findings {
		fmt.Fprintln(a.Stdout, f)
	}
	if len(findings) > 0 {
		return errQuiet
	}
	return nil
}

var ageRE = regexp.MustCompile(`^(\d+)([dw])$`)

// parseAge reads "3d" or "2w".
func parseAge(s string) (time.Duration, bool) {
	m := ageRE.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	if m[2] == "w" {
		n *= 7
	}
	return time.Duration(n) * 24 * time.Hour, true
}

func parseWhen(s string, now time.Time) (store.Date, error) {
	today := store.DateOf(now)
	switch s = strings.ToLower(s); s {
	case "today":
		return today, nil
	case "yesterday":
		return store.DateOf(today.AddDate(0, 0, -1)), nil
	}
	if d, ok := parseAge(s); ok {
		return store.DateOf(today.Add(-d)), nil
	}
	for wd := time.Sunday; wd <= time.Saturday; wd++ {
		name := strings.ToLower(wd.String())
		if s == name || s == name[:3] {
			back := (int(today.Weekday()) - int(wd) + 7) % 7
			return store.DateOf(today.AddDate(0, 0, -back)), nil
		}
	}
	d, err := store.ParseDate(s)
	if err != nil {
		return store.Date{}, fmt.Errorf("invalid date %q; see wip help", s)
	}
	return d, nil
}
