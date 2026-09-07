package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/lvim-tech/clouder/internal/sync"
	"github.com/lvim-tech/clouder/internal/theme"
)

// styles is every lipgloss style the interface draws with, compiled once from a
// resolved theme (the lvim-colorscheme palette). Built in newStyles, then only
// read — a theme change means a new styles, never mutation.
type styles struct {
	Title     lipgloss.Style
	Tab       lipgloss.Style
	TabActive lipgloss.Style
	Heading   lipgloss.Style
	Key       lipgloss.Style
	Muted     lipgloss.Style
	Text      lipgloss.Style
	Cursor    lipgloss.Style
	Sel       lipgloss.Style
	OK        lipgloss.Style
	Warn      lipgloss.Style
	Err       lipgloss.Style
	FieldOn   lipgloss.Style
	URL       lipgloss.Style
	Accent    lipgloss.Style
	AccentAlt lipgloss.Style
	Cyan      lipgloss.Style
	Yellow    lipgloss.Style
}

func adaptive(c theme.Color) lipgloss.TerminalColor {
	if c.IsZero() {
		return nil
	}
	return lipgloss.AdaptiveColor{Light: c.Light, Dark: c.Dark}
}

func fg(s lipgloss.Style, c lipgloss.TerminalColor) lipgloss.Style {
	if c == nil {
		return s
	}
	return s.Foreground(c)
}

func newStyles(t theme.Theme) styles {
	c := t.Colors
	accent := adaptive(c.Accent)
	accentAlt := adaptive(c.AccentAlt)
	text := adaptive(c.Text)
	muted := adaptive(c.Muted)
	success := adaptive(c.Success)
	warning := adaptive(c.Warning)
	failure := adaptive(c.Error)
	titleFg := adaptive(c.TitleFg)
	cyan := adaptive(c.Cyan)
	if cyan == nil {
		cyan = lipgloss.AdaptiveColor{Light: "#0e7490", Dark: "#22d3ee"}
	}
	yellow := adaptive(c.Yellow)
	if yellow == nil {
		yellow = lipgloss.AdaptiveColor{Light: "#8a6d00", Dark: "#e5c07b"}
	}

	title := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	title = fg(title, titleFg)
	if accent != nil {
		title = title.Background(accent)
	}
	tabActive := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	tabActive = fg(tabActive, titleFg)
	if accentAlt != nil {
		tabActive = tabActive.Background(accentAlt)
	}

	s := styles{
		Title:     title,
		Tab:       fg(lipgloss.NewStyle().Padding(0, 1), muted),
		TabActive: tabActive,
		Heading:   fg(lipgloss.NewStyle().Bold(true), accentAlt),
		Key:       fg(lipgloss.NewStyle().Bold(true), accent),
		Muted:     fg(lipgloss.NewStyle(), muted),
		Text:      fg(lipgloss.NewStyle(), text),
		Cursor:    fg(lipgloss.NewStyle().Bold(true), accent),
		Sel:       lipgloss.NewStyle().Bold(true),
		OK:        fg(lipgloss.NewStyle(), success),
		Warn:      fg(lipgloss.NewStyle(), warning),
		Err:       fg(lipgloss.NewStyle().Bold(true), failure),
		FieldOn:   fg(lipgloss.NewStyle().Bold(true), accent),
		URL:       fg(lipgloss.NewStyle().Underline(true), accent),
		Accent:    fg(lipgloss.NewStyle(), accent),
		AccentAlt: fg(lipgloss.NewStyle(), accentAlt),
		Cyan:      fg(lipgloss.NewStyle(), cyan),
		Yellow:    fg(lipgloss.NewStyle(), yellow),
	}
	if accent == nil { // mono: emphasis by reverse/bold
		s.Title = s.Title.Reverse(true)
		s.Cursor = s.Cursor.Reverse(true)
	}
	if accentAlt == nil {
		s.TabActive = s.TabActive.Reverse(true)
	}
	if muted == nil {
		s.Muted = s.Muted.Faint(true)
		s.Tab = s.Tab.Faint(true)
	}
	return s
}

// markStyle colours a diff mark: an in-sync "=" in blue, and every difference
// (any direction, or a conflict) in red — so the eye reads "same vs changed"
// at a glance.
func (s styles) markStyle(k sync.Kind) lipgloss.Style {
	if k == sync.InSync {
		return s.Accent // blue
	}
	return s.Err // red
}
