package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/lvim-tech/clouder/internal/config"
	"github.com/lvim-tech/clouder/internal/diff"
	"github.com/lvim-tech/clouder/internal/sync"
)

const (
	layoutSec = "2006-01-02 15:04:05" // 19 cols
	layoutMin = "2006-01-02 15:04"    // 16 cols
	minPathW  = 14                    // keep dates alive on a narrow split; path truncates
)

// sideFormat chooses the timestamp+size column format for the current width and
// the seconds preference — the responsive step. It degrades: seconds → minute →
// size-only, so the two side columns always leave room for the path.
func (m Model) sideFormat() (layout string, sideW int) {
	if m.showSeconds {
		layout, sideW = layoutSec, 29
	} else {
		layout, sideW = layoutMin, 26
	}
	fits := func(sw int) bool { return 6+2+2+minPathW+1+sw+2+sw <= m.width }
	if !fits(sideW) {
		layout, sideW = layoutMin, 26
	}
	if !fits(sideW) {
		layout, sideW = "", 9 // size only
	}
	return layout, sideW
}

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(m.tabBar() + "\n\n")

	switch {
	case m.overlay == ovPair:
		b.WriteString(m.viewPair())
	case m.overlay == ovProvider:
		b.WriteString(m.viewProvider())
	case m.overlay == ovAccount:
		b.WriteString(m.viewAccount())
	case m.isSettingsTab():
		b.WriteString(m.viewSettings())
	default:
		b.WriteString(m.viewSync())
	}

	b.WriteString("\n")
	if m.status != "" {
		if m.stErr {
			b.WriteString(m.st.Err.Render("✗ " + m.status))
		} else {
			b.WriteString(m.st.OK.Render("• " + m.status))
		}
		b.WriteString("\n")
	}

	// Normalize to exactly the pane height: pad short frames and clip long ones,
	// so each redraw fully overwrites the last and nothing ghosts underneath.
	out := b.String()
	if m.height > 0 {
		lines := strings.Split(out, "\n")
		if len(lines) > m.height {
			lines = lines[:m.height]
		} else {
			for len(lines) < m.height {
				lines = append(lines, "")
			}
		}
		out = strings.Join(lines, "\n")
	}
	return out
}

func (m Model) tabBar() string {
	title := m.st.Title.Render(" clouder ")
	active := m.st.Accent.Bold(true).Underline(true)
	idle := m.st.Muted

	labels := make([]string, 0, len(m.cfg.Pairs)+1)
	for _, p := range m.cfg.Pairs {
		labels = append(labels, p.Name)
	}
	labels = append(labels, "Settings")
	activeIdx := m.activeTab // Settings sits at len(pairs)

	// Responsive: fit a window of tabs around the active one into the width,
	// with ‹ › markers when some are off-screen — the same trim the desktop's
	// other TUIs do on a narrow terminal.
	const sep = "  ·  "
	suffixW := 0
	if m.dirty {
		suffixW += runeLen("   • unsaved")
	}
	if m.force {
		suffixW += runeLen("   force")
	}
	avail := m.width - runeLen(" clouder ") - 2 - suffixW - 4 // 4 spare for ‹ ›
	if avail < 12 {
		avail = 12
	}

	lo, hi := activeIdx, activeIdx
	width := runeLen(labels[activeIdx])
	for {
		grew := false
		if hi+1 < len(labels) {
			if w := width + runeLen(sep) + runeLen(labels[hi+1]); w <= avail {
				hi++
				width = w
				grew = true
			}
		}
		if lo-1 >= 0 {
			if w := width + runeLen(sep) + runeLen(labels[lo-1]); w <= avail {
				lo--
				width = w
				grew = true
			}
		}
		if !grew {
			break
		}
	}

	var parts []string
	for i := lo; i <= hi; i++ {
		if i == activeIdx && m.overlay == ovNone {
			parts = append(parts, active.Render(labels[i]))
		} else {
			parts = append(parts, idle.Render(labels[i]))
		}
	}
	strip := strings.Join(parts, m.st.Muted.Render(sep))
	if lo > 0 {
		strip = m.st.Muted.Render("‹ ") + strip
	}
	if hi < len(labels)-1 {
		strip = strip + m.st.Muted.Render(" ›")
	}

	line := title + "  " + strip
	if m.dirty {
		line += "   " + m.st.Err.Render("• unsaved")
	}
	if m.force {
		line += "   " + m.st.AccentAlt.Render("force")
	}
	return line
}

func (m Model) hint(keys ...[2]string) string {
	var parts []string
	for _, k := range keys {
		parts = append(parts, m.st.Key.Render("["+k[0]+"]")+" "+m.st.Muted.Render(k[1]))
	}
	return strings.Join(parts, "   ")
}

// --- Sync tab ---

func (m Model) viewSync() string {
	var b strings.Builder
	if len(m.cfg.Pairs) == 0 {
		b.WriteString(m.st.Muted.Render("No projects yet. Press a to add one, or A on Settings to log in an account.") + "\n")
		b.WriteString("\n" + m.hint([2]string{"a", "add project"}, [2]string{"tab", "settings"}, [2]string{"q", "quit"}) + "\n")
		return b.String()
	}
	p, _ := m.selectedPair()
	ps := m.states[p.Name]

	// Info line for the active tab: the path route, then the change counts.
	route := m.st.Text.Render(config.Expand(p.Local)) + m.st.Muted.Render("  ⇄  ") +
		m.st.Text.Render(fmt.Sprintf("%s:%s:%s", p.Provider, p.Account, remoteLabel(p.Remote)))
	b.WriteString(route + "   " + m.statusChip(ps) + "\n\n")

	for _, line := range m.renderPane(p) {
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	b.WriteString(m.hintGroup("Files  ", m.st.OK,
		[2]string{"↑↓", "move"}, [2]string{"space", "select"}, [2]string{"v", "all"},
		[2]string{"s", "sync sel"}, [2]string{"u/d", "up/down sel"}, [2]string{"n", "dry-run"}, [2]string{"f", "force"}, [2]string{"t", "seconds"},
	) + "\n")
	b.WriteString(m.hintGroup("Project", m.st.AccentAlt,
		[2]string{"←→", "switch"}, [2]string{"r", "rescan"}, [2]string{"a", "add"}, [2]string{"e", "edit"}, [2]string{"x", "delete"}, [2]string{"w", "save"}, [2]string{"q", "quit"},
	) + "\n")
	return b.String()
}

func (m Model) statusChip(ps *pairState) string {
	if ps == nil || ps.mode == modeNone && !ps.busy && ps.err == nil {
		return m.st.Muted.Render("· not scanned")
	}
	if ps.busy {
		return m.st.AccentAlt.Render("· " + ps.busyWhat + "…")
	}
	if ps.err != nil {
		return m.st.Err.Render("· error")
	}
	t := sync.Count(ps.changes)
	up := t[sync.UpNew] + t[sync.UpMod] + t[sync.UpDel]
	down := t[sync.DownNew] + t[sync.DownMod] + t[sync.DownDel]
	conf := t[sync.Conflict]
	parts := []string{
		m.st.Accent.Render(fmt.Sprintf("↑%d", up)),
		m.st.AccentAlt.Render(fmt.Sprintf("↓%d", down)),
	}
	if conf > 0 {
		parts = append(parts, m.st.Err.Render(fmt.Sprintf("!%d", conf)))
	}
	if up+down+conf == 0 {
		return m.st.OK.Render("· in sync")
	}
	return strings.Join(parts, " ")
}

func (m Model) renderPane(p config.Pair) []string {
	ps := m.states[p.Name]
	if ps == nil || (ps.mode == modeNone && !ps.busy && ps.err == nil) {
		return []string{m.st.Muted.Render("   press r to scan this pair")}
	}
	if ps.busy {
		return []string{m.st.AccentAlt.Render("   " + ps.busyWhat + "…")}
	}
	if ps.err != nil {
		return wrapIndent(m.st.Err.Render("   "+ps.err.Error()), m.width)
	}
	if ps.mode == modePlan {
		return m.renderPlan(ps)
	}
	return m.renderDiff(ps)
}

func (m Model) renderDiff(ps *pairState) []string {
	rows := ps.rows
	if len(rows) == 0 {
		return []string{m.st.OK.Render("   ✓ empty — nothing here on either side")}
	}

	layout, sideW := m.sideFormat()
	pathW := minPathW
	for _, r := range rows {
		if l := lipgloss.Width(r.Path); l > pathW {
			pathW = l
		}
	}
	avail := m.width - 6 - 2 - 2 - 1 - sideW - 2 - sideW
	if avail < minPathW {
		avail = minPathW
	}
	if pathW > avail {
		pathW = avail
	}

	// A scrolling window so the whole tree fits: keep the cursor in view. The
	// budget is the pane height minus the fixed chrome (tabs, route, header,
	// summary, two hint rows, status, margins).
	visible := m.height - 14
	if visible < 4 {
		visible = 4
	}
	start := 0
	if len(rows) > visible {
		start = ps.cur - visible/2
		if start < 0 {
			start = 0
		}
		if start > len(rows)-visible {
			start = len(rows) - visible
		}
	}
	end := start + visible
	if end > len(rows) {
		end = len(rows)
	}

	filesFocused := true
	var out []string
	// Row gutter before ST is 6 cols: "  " + cursor(2) + sel(1) + " ". The
	// header must lead with the same 6 so the titles sit over their columns.
	// Colours: PATH names blue, LOCAL green, REMOTE cyan.
	// Everything left-aligned at each column's start — labels, sizes, folders —
	// so a title sits directly over the value beneath it regardless of the
	// value's length (LOCAL/REMOTE differ in length, which right-align would
	// stagger).
	head := "      " + m.st.Muted.Render(fmt.Sprintf("%-2s  %-*s ", "ST", pathW, "PATH")) +
		m.st.Yellow.Render(fmt.Sprintf("%-*s", sideW, "LOCAL")) + "  " +
		m.st.Cyan.Render(fmt.Sprintf("%-*s", sideW, "REMOTE"))
	out = append(out, head)
	if start > 0 {
		out = append(out, "   "+m.st.Muted.Render(fmt.Sprintf("↑ %d more", start)))
	}
	for i := start; i < end; i++ {
		r := rows[i]
		isDir := strings.HasSuffix(r.Path, "/")
		cursor := "  "
		if filesFocused && i == ps.cur {
			cursor = m.st.Cursor.Render("▌ ")
		}
		// Only changed rows are selectable; an in-sync row shows no marker
		// (it is there for information, not for syncing).
		selGlyph := " "
		if r.Kind != sync.InSync {
			selGlyph = m.st.Muted.Render("·")
			if ps.sel[r.Path] {
				selGlyph = m.st.Accent.Render("◉")
			}
		}
		mk := m.st.markStyle(r.Kind).Render(padCells(diff.Mark(r.Kind), 2))
		lt, rt := timeOf(r.Local), timeOf(r.Remote)
		// Same content ⇒ one edit time: an in-sync file shows the same moment on
		// both sides, so a stale local mtime (a download's timestamp) never
		// reads as a difference where there is none.
		if r.Kind == sync.InSync && r.Local != nil && r.Remote != nil {
			u := r.Remote.Modified
			if u.IsZero() {
				u = r.Local.Modified
			}
			lt, rt = u, u
		}
		line := "  " + cursor + selGlyph + " " + mk + "  " + m.st.Accent.Render(padPath(r.Path, pathW)) + " " +
			m.st.Yellow.Render(cellAt(r.Local, isDir, lt, layout, sideW)) + "  " + m.st.Cyan.Render(cellAt(r.Remote, isDir, rt, layout, sideW))
		if r.Kind == sync.Conflict {
			line += "  " + m.st.Err.Render("CONFLICT")
		}
		out = append(out, line)
	}
	if end < len(rows) {
		out = append(out, "   "+m.st.Muted.Render(fmt.Sprintf("↓ %d more", len(rows)-end)))
	}
	out = append(out, "")
	out = append(out, "   "+m.summaryLine(ps.changes))
	if n := len(selectedPaths(ps)); n > 0 {
		out = append(out, "   "+m.st.Accent.Render(fmt.Sprintf("%d selected — s/u/d act on the selection", n)))
	}
	return out
}

func (m Model) renderPlan(ps *pairState) []string {
	out := []string{"   " + m.st.Heading.Render("dry-run — would do:")}
	shown := false
	for _, a := range ps.actions {
		if a.Skipped || a.Detail == "" {
			continue
		}
		shown = true
		out = append(out, "   "+m.st.Accent.Render("•")+" "+m.st.Text.Render(fmt.Sprintf("%-32s", a.Name))+m.st.Muted.Render(a.Detail))
	}
	if !shown {
		out = append(out, "   "+m.st.OK.Render("nothing to do"))
	}
	return out
}

func (m Model) summaryLine(changes []sync.Change) string {
	t := sync.Count(changes)
	up := t[sync.UpNew] + t[sync.UpMod] + t[sync.UpDel]
	down := t[sync.DownNew] + t[sync.DownMod] + t[sync.DownDel]
	return m.st.Accent.Render(fmt.Sprintf("↑ up %d", up)) + "   " +
		m.st.AccentAlt.Render(fmt.Sprintf("↓ down %d", down)) + "   " +
		m.st.Muted.Render(fmt.Sprintf("= %d", t[sync.InSync])) + "   " +
		m.st.Err.Render(fmt.Sprintf("! %d", t[sync.Conflict]))
}

// --- Settings tab ---

func (m Model) viewSettings() string {
	var b strings.Builder
	b.WriteString(m.st.Heading.Render("Provider") + "\n")
	key := m.cfg.Settings("dropbox")["app_key"]
	b.WriteString("  " + m.st.Muted.Render("dropbox app_key: ") + m.st.Text.Render(maskKey(key)) + "\n\n")

	b.WriteString(m.st.Heading.Render("Accounts") + "\n")
	accts := m.knownAccounts()
	tokened := map[string]bool{}
	if keys, err := m.secrets.Keys(secretPrefixDropbox); err == nil {
		for _, k := range keys {
			if i := strings.LastIndex(k, "/"); i >= 0 {
				tokened[k[i+1:]] = true
			}
		}
	}
	if len(accts) == 0 {
		b.WriteString("  " + m.st.Muted.Render("none yet — press A to add and log one in") + "\n")
	}
	for i, a := range accts {
		cur := "  "
		name := m.st.Text.Render(a)
		if i == m.acctCursor {
			cur = m.st.Cursor.Render("▌ ")
			name = m.st.Sel.Render(a)
		}
		mark := m.st.Warn.Render("no token")
		if tokened[a] {
			mark = m.st.OK.Render("✓ logged in")
		}
		b.WriteString("  " + cur + name + "  " + mark + "\n")
	}
	b.WriteString("\n")
	b.WriteString(m.st.Muted.Render("  theme: "+themeName(m.cfg.Theme.Name)) + "\n\n")
	b.WriteString(m.hint(
		[2]string{"p", "set app_key"}, [2]string{"A", "add"}, [2]string{"e", "re-login"}, [2]string{"x", "remove"}, [2]string{"↑↓", "pick"},
	) + "\n")
	b.WriteString(m.hint(
		[2]string{"w", "save"}, [2]string{"tab", "sync"}, [2]string{"q", "quit"},
	) + "\n")
	return b.String()
}

const secretPrefixDropbox = "clouder/dropbox"

// hintGroup renders a labeled group of key hints, the label in `label` style so
// the file-level and project-level controls read as two distinct sets.
func (m Model) hintGroup(label string, labelStyle lipgloss.Style, keys ...[2]string) string {
	return labelStyle.Bold(true).Render(label) + "  " + m.hint(keys...)
}

// --- overlays ---

func (m Model) viewPair() string {
	var b strings.Builder
	head := "New project (pair)"
	if m.editName != "" {
		head = "Edit project: " + m.editName
	}
	b.WriteString(m.st.Heading.Render(head) + "\n\n")
	for i := range m.fields {
		label := m.st.Muted.Render(fmt.Sprintf("%-18s", fieldLabels[i]))
		if i == m.focus {
			label = m.st.FieldOn.Render(fmt.Sprintf("%-18s", fieldLabels[i]))
		}
		b.WriteString("  " + label + " " + m.fields[i].View() + "\n")
	}
	b.WriteString("\n")
	b.WriteString(m.st.Muted.Render("  provider ∈ {"+strings.Join(providerNames(), ", ")+"};  local takes ~ / __HOME__;  remote \"\" or \"/\" = account root") + "\n\n")
	b.WriteString(m.hint([2]string{"tab", "next"}, [2]string{"↵", "next / save"}, [2]string{"esc", "cancel"}) + "\n")
	return b.String()
}

func (m Model) viewProvider() string {
	var b strings.Builder
	b.WriteString(m.st.Heading.Render("Provider key: "+m.provName) + "\n\n")
	b.WriteString("  " + m.st.FieldOn.Render(fmt.Sprintf("%-10s", "app_key")) + " " + m.appKey.View() + "\n\n")
	b.WriteString(m.st.Muted.Render("  From your Dropbox app (Scoped access, PKCE — no secret).") + "\n")
	b.WriteString(m.st.Muted.Render("  Create at https://www.dropbox.com/developers/apps") + "\n\n")
	b.WriteString(m.hint([2]string{"↵", "set"}, [2]string{"esc", "cancel"}) + "\n")
	return b.String()
}

func (m Model) viewAccount() string {
	var b strings.Builder
	b.WriteString(m.st.Heading.Render("Add / log in account") + "\n\n")
	if m.acctStage == 0 {
		pl, nl := fmt.Sprintf("%-10s", "provider"), fmt.Sprintf("%-10s", "account")
		if m.focus == 0 {
			b.WriteString("  " + m.st.FieldOn.Render(pl) + " " + m.acctProv.View() + "\n")
			b.WriteString("  " + m.st.Muted.Render(nl) + " " + m.acctName.View() + "\n\n")
		} else {
			b.WriteString("  " + m.st.Muted.Render(pl) + " " + m.acctProv.View() + "\n")
			b.WriteString("  " + m.st.FieldOn.Render(nl) + " " + m.acctName.View() + "\n\n")
		}
		b.WriteString(m.st.Muted.Render("  Set the provider's app_key first (Settings → p).") + "\n\n")
		b.WriteString(m.hint([2]string{"tab", "switch"}, [2]string{"↵", "start login"}, [2]string{"esc", "cancel"}) + "\n")
		return b.String()
	}
	b.WriteString(m.st.Text.Render("  1. Open this URL and approve:") + "\n\n")
	b.WriteString("     " + m.st.URL.Render(m.authURL) + "\n\n")
	b.WriteString(m.st.Text.Render("  2. Paste the code Dropbox shows:") + "\n\n")
	b.WriteString("  " + m.st.FieldOn.Render(fmt.Sprintf("%-6s", "code")) + " " + m.code.View() + "\n\n")
	b.WriteString(m.hint([2]string{"↵", "finish login"}, [2]string{"esc", "cancel"}) + "\n")
	return b.String()
}

// --- small format helpers ---

func cellAt(s *sync.Side, isDir bool, t time.Time, layout string, sideW int) string {
	// All left-aligned at the column start so the LOCAL/REMOTE titles sit
	// directly over their values.
	if s == nil {
		return fmt.Sprintf("%-*s", sideW, "—")
	}
	if isDir {
		return fmt.Sprintf("%-*s", sideW, "folder")
	}
	if layout == "" { // narrow: size only
		return fmt.Sprintf("%-*s", sideW, diff.HumanSize(s.Size))
	}
	return fmt.Sprintf("%s  %8s", t.Local().Format(layout), diff.HumanSize(s.Size))
}

func timeOf(s *sync.Side) time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.Modified
}

func remoteLabel(r string) string {
	if strings.TrimSpace(r) == "" {
		return "/"
	}
	return r
}

func maskKey(k string) string {
	if k == "" {
		return "(not set)"
	}
	if len(k) <= 4 {
		return "****"
	}
	return k[:2] + strings.Repeat("•", len(k)-4) + k[len(k)-2:]
}

func themeName(n string) string {
	if n == "" {
		return "default"
	}
	return n
}

func changedCount(changes []sync.Change) int {
	n := 0
	for _, c := range changes {
		if c.Kind != sync.InSync {
			n++
		}
	}
	return n
}

func padPath(p string, w int) string {
	p = middleTruncate(p, w)
	if pad := w - lipgloss.Width(p); pad > 0 {
		return p + strings.Repeat(" ", pad)
	}
	return p
}

// padCells right-pads s with spaces to w display columns (lipgloss's own width
// metric), so a glyph the font renders wider than one cell can't shift the
// columns that follow.
func padCells(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
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
	return string(r[:left]) + "…" + string(r[len(r)-(w-1-left):])
}

func runeLen(s string) int { return len([]rune(s)) }

func wrapIndent(s string, width int) []string {
	if width <= 6 {
		return []string{s}
	}
	return []string{s}
}
