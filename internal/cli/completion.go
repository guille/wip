package cli

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"
)

//go:embed completion.zsh
var zshCompletion string

func (a *App) completion(args []string) error {
	pos, err := parse(a.flags("completion"), args, 1, 1)
	if err != nil {
		return err
	}
	if pos[0] != "zsh" {
		return fmt.Errorf("completion: only zsh is supported")
	}
	_, err = fmt.Fprint(a.Stdout, zshCompletion)
	return err
}

// ids lists "ID<TAB>STATE<TAB>TITLE" for shell completion: open items, or
// every item including archived ones with --all.
func (a *App) ids(args []string) error {
	fs := a.flags("_ids")
	all := fs.Bool("all", false, "")
	if _, err := parse(fs, args, 0, 0); err != nil {
		return err
	}
	items, _ := a.Store.List(*all)
	var b strings.Builder
	for _, it := range items {
		if *all || it.State.Open() {
			fmt.Fprintf(&b, "%s\t%s\t%s\n", it.ID, stateLabel(it), it.Title)
		}
	}
	_, err := fmt.Fprint(a.Stdout, b.String())
	return err
}

// scopes lists every scope in use, archived items included, for shell completion.
func (a *App) scopes(args []string) error {
	if _, err := parse(a.flags("_scopes"), args, 0, 0); err != nil {
		return err
	}
	items, _ := a.Store.List(true)
	var scopes []string
	for _, it := range items {
		scopes = append(scopes, it.Scope)
	}
	slices.Sort(scopes)
	var b strings.Builder
	for _, s := range slices.Compact(scopes) {
		fmt.Fprintln(&b, s)
	}
	_, err := fmt.Fprint(a.Stdout, b.String())
	return err
}
