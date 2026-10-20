package cli

import (
	"os"
	"strings"

	"charm.land/lipgloss/v2"

	"go.guillerg.dev/wip/internal/store"
)

// palette styles output for a terminal. The zero value is plain text, so
// pipes and agents see exactly the same words without escape codes.
type palette struct{ on bool }

func colorFor(f *os.File) palette {
	if os.Getenv("NO_COLOR") != "" {
		return palette{}
	}
	fi, err := f.Stat()
	return palette{err == nil && fi.Mode()&os.ModeCharDevice != 0}
}

var (
	styleBold    = lipgloss.NewStyle().Bold(true)
	styleFaint   = lipgloss.NewStyle().Faint(true)
	styleWaitMe  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Yellow)
	styleWait    = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	styleBlocked = lipgloss.NewStyle().Foreground(lipgloss.Red)
	styleDone    = lipgloss.NewStyle().Foreground(lipgloss.Green)
)

func (p palette) paint(s lipgloss.Style, text string) string {
	if !p.on || text == "" {
		return text
	}
	return s.Render(text)
}

func (p palette) id(text string) string    { return p.paint(styleBold, text) }
func (p palette) faint(text string) string { return p.paint(styleFaint, text) }

// state paints text according to the item's state.
func (p palette) state(it *store.Item, text string) string {
	switch {
	case it.State == store.Waiting && it.WaitingOn == store.Me:
		return p.paint(styleWaitMe, text)
	case it.State == store.Waiting:
		return p.paint(styleWait, text)
	case it.State == store.Done:
		return p.paint(styleDone, text)
	case it.State == store.Dropped:
		return p.paint(styleFaint, text)
	}
	return text
}

// cell is a table cell: plain text for measuring, painted text for printing.
type cell struct{ plain, out string }

// table aligns cells by their plain width, two spaces apart. The last column
// is left unpadded.
func table(rows [][]cell) string {
	var widths []int
	for _, r := range rows {
		for i, c := range r {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], lipgloss.Width(c.plain))
		}
	}
	var b strings.Builder
	for _, r := range rows {
		for i, c := range r {
			b.WriteString(c.out)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-lipgloss.Width(c.plain)+2))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}
