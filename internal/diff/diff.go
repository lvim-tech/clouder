// Package diff renders a reconcile result as the two-column view: one row per
// path, a state/direction mark, then the local and remote sides each with a
// last-modified date and a human-readable size. It returns lines (given a
// width) so both the CLI and the Phase-2 TUI draw from one renderer.
package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lvim-tech/clouder/internal/sync"
)

const (
	sideWidth = 26 // "2006-01-02 15:04" (16) + "  " (2) + size right-aligned (8)
	minPath   = 24
	dateLayout = "2006-01-02 15:04"

	red   = "\x1b[31m"
	dim   = "\x1b[2m"
	reset = "\x1b[0m"
)

// Mark is the two-cell state/direction glyph for a change kind.
func Mark(k sync.Kind) string { return mark(k) }

func mark(k sync.Kind) string {
	switch k {
	case sync.InSync:
		return "="
	case sync.UpNew:
		return "↑+"
	case sync.UpMod:
		return "↑~"
	case sync.UpDel:
		return "↑✕"
	case sync.DownNew:
		return "↓+"
	case sync.DownMod:
		return "↓~"
	case sync.DownDel:
		return "↓✕"
	case sync.Conflict:
		return "!"
	}
	return "?"
}

// HumanSize formats a byte count: "0 B", "999 B", then KiB/MiB/GiB/TiB with one
// decimal below 10 and none at or above 10.
func HumanSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	const units = "KMGTPE"
	v := float64(n)
	i := -1
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	unit := string(units[i]) + "iB"
	if v < 10 {
		return fmt.Sprintf("%.1f %s", v, unit)
	}
	return fmt.Sprintf("%.0f %s", v, unit)
}

// Render turns a reconcile result into terminal lines. When showInSync is
// false, `=` rows are omitted (the counts line still reports them). color adds
// ANSI (red conflicts); pass false for a plain capture.
func Render(label string, changes []sync.Change, width int, showInSync, color bool) []string {
	if width <= 0 {
		width = 100
	}
	rows := make([]sync.Change, 0, len(changes))
	for _, c := range changes {
		if c.Kind == sync.InSync && !showInSync {
			continue
		}
		rows = append(rows, c)
	}
	sort.SliceStable(rows, func(i, j int) bool { return less(rows[i].Path, rows[j].Path) })

	longest := minPath
	for _, r := range rows {
		if l := runeLen(r.Path); l > longest {
			longest = l
		}
	}
	avail := width - 3 /*indent*/ - 2 /*mark*/ - 2 /*gap*/ - 1 /*gap*/ - sideWidth - 2 - sideWidth
	pathW := longest
	if avail >= minPath && pathW > avail {
		pathW = avail
	}
	if pathW < minPath {
		pathW = minPath
	}

	var out []string
	if label != "" {
		out = append(out, label)
	}
	header := fmt.Sprintf("   %-2s  %-*s %-*s  %-*s", "ST", pathW, "PATH", sideWidth, "LOCAL", sideWidth, "REMOTE")
	out = append(out, header)

	for _, r := range rows {
		line := fmt.Sprintf("   %-2s  %s %s  %s",
			mark(r.Kind), padPath(r.Path, pathW), cell(r.Local), cell(r.Remote))
		if r.Kind == sync.Conflict {
			line += "  CONFLICT"
			if color {
				line = red + line + reset
			}
		}
		out = append(out, line)
	}
	out = append(out, "")
	out = append(out, SummaryLine(changes))
	if Count(changes)[sync.Conflict] > 0 {
		out = append(out, "conflicts left alone — resolve with --up --force or --down --force")
	}
	return out
}

func cell(s *sync.Side) string {
	if s == nil {
		return fmt.Sprintf("%-*s", sideWidth, "—")
	}
	return fmt.Sprintf("%s  %8s", s.Modified.Local().Format(dateLayout), HumanSize(s.Size))
}

// Count buckets changes by kind (re-exported convenience over sync.Count).
func Count(changes []sync.Change) map[sync.Kind]int { return sync.Count(changes) }

// SummaryLine is the one-line tally under a diff.
func SummaryLine(changes []sync.Change) string {
	t := sync.Count(changes)
	up := t[sync.UpNew] + t[sync.UpMod] + t[sync.UpDel]
	down := t[sync.DownNew] + t[sync.DownMod] + t[sync.DownDel]
	return fmt.Sprintf("↑ up %d   ↓ down %d   = in sync %d   ! conflicts %d",
		up, down, t[sync.InSync], t[sync.Conflict])
}

func padPath(p string, w int) string {
	p = middleTruncate(p, w)
	return p + strings.Repeat(" ", w-runeLen(p))
}

func middleTruncate(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	left := (w - 1) / 2
	right := w - 1 - left
	return string(r[:left]) + "…" + string(r[len(r)-right:])
}

func runeLen(s string) int { return len([]rune(s)) }

// less groups by directory prefix, then by name, so a folder's files stay
// together instead of scattering through a flat alphabetical list.
func less(a, b string) bool {
	da, na := split(a)
	db, nb := split(b)
	if da != db {
		return da < db
	}
	return na < nb
}

func split(p string) (dir, name string) {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i], p[i+1:]
	}
	return "", p
}
