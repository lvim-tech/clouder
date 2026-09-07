// Package tui is clouder's interactive interface — everything in one place, no
// dropping to the shell. Two tabs: Sync (pick a pair, see its two-column diff,
// run the sync both ways) and Settings (provider key, log an account in, manage
// pairs). Coloured with the lvim-colorscheme palette, like the rest of the
// desktop. It is the default view when clouder runs with no verb.
package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lvim-tech/clouder/internal/config"
	"github.com/lvim-tech/clouder/internal/engine"
	"github.com/lvim-tech/clouder/internal/provider"
	"github.com/lvim-tech/clouder/internal/secret"
	"github.com/lvim-tech/clouder/internal/sync"
	"github.com/lvim-tech/clouder/internal/theme"
)

type overlay int

const (
	ovNone overlay = iota
	ovPair
	ovProvider
	ovAccount
)

// pair-form field indices
const (
	fName = iota
	fLocal
	fProvider
	fAccount
	fRemote
	fIgnore
	nFields
)

var fieldLabels = [nFields]string{"name", "local folder", "provider", "account", "remote folder", "ignore (comma-sep)"}

// paneMode is what the right side of the Sync tab is showing for a pair.
type paneMode int

const (
	modeNone paneMode = iota // never scanned
	modeDiff                 // showing a reconcile
	modePlan                 // showing a dry-run's would-actions
)

type pairState struct {
	mode     paneMode
	changes  []sync.Change
	rows     []sync.Change   // actionable changes (non-InSync), the navigable list
	cur      int             // cursor into rows
	sel      map[string]bool // selected paths (empty ⇒ act on all)
	skipped  []string
	actions  []sync.Action
	busy     bool
	busyWhat string
	err      error
}

// Model is the whole interface.
type Model struct {
	cfg     config.Config
	secrets secret.Store
	st      styles

	overlay overlay
	// activeTab indexes the tab strip: 0..len(pairs)-1 are pair tabs,
	// len(pairs) is the Settings tab.
	activeTab int
	states    map[string]*pairState

	// settings-tab account list
	acctCursor int

	width, height int
	status        string
	stErr         bool
	dirty         bool
	force         bool
	showSeconds   bool

	fields   [nFields]textinput.Model
	focus    int
	editName string

	appKey   textinput.Model
	provName string

	acctProv  textinput.Model
	acctName  textinput.Model
	code      textinput.Model
	flow      provider.AuthFlow
	authURL   string
	acctStage int
}

// New builds the model, compiling styles from the configured theme.
func New(cfg config.Config, secrets secret.Store) Model {
	m := Model{
		cfg:     cfg,
		secrets: secrets,
		st:      newStyles(theme.Resolve(cfg.Theme.Name)),
		states:  map[string]*pairState{},
		width:   100,
		height:  30,

		showSeconds: !cfg.View.HideSeconds,
	}
	for i := range m.fields {
		ti := textinput.New()
		ti.CharLimit = 512
		ti.Width = 58
		m.fields[i] = ti
	}
	m.appKey = textinput.New()
	m.appKey.CharLimit = 200
	m.appKey.Width = 58
	m.acctProv = textinput.New()
	m.acctProv.Width = 30
	m.acctName = textinput.New()
	m.acctName.Width = 30
	m.code = textinput.New()
	m.code.Width = 70
	m.code.CharLimit = 512
	// Auto-scan the first pair on open.
	if len(cfg.Pairs) > 0 {
		m.states[cfg.Pairs[0].Name] = &pairState{busy: true, busyWhat: "scanning"}
	}
	return m
}

func (m Model) Init() tea.Cmd {
	if len(m.cfg.Pairs) > 0 {
		return tea.Batch(textinput.Blink, m.scanCmd(m.cfg.Pairs[0].Name))
	}
	return textinput.Blink
}

func (m Model) isSettingsTab() bool { return m.activeTab >= len(m.cfg.Pairs) }

// switchTab moves to tab index i (clamped) and auto-scans a pair tab that has
// not been scanned yet.
func (m Model) switchTab(i int) (Model, tea.Cmd) {
	n := len(m.cfg.Pairs)
	if i < 0 {
		i = 0
	}
	if i > n { // n == Settings tab
		i = n
	}
	m.activeTab = i
	if i < n {
		p := m.cfg.Pairs[i]
		ps := m.states[p.Name]
		if ps == nil || (ps.mode == modeNone && !ps.busy && ps.err == nil) {
			nps := m.state(p.Name)
			nps.busy, nps.busyWhat = true, "scanning"
			return m, m.scanCmd(p.Name)
		}
	}
	return m, nil
}

func providerNames() []string { return provider.Names() }

// Run starts the program.
func Run(cfg config.Config, secrets secret.Store) error {
	_, err := tea.NewProgram(New(cfg, secrets), tea.WithAltScreen()).Run()
	return err
}

// --- async messages ---

type scannedMsg struct {
	pair string
	r    engine.Reconciled
	err  error
}

type syncedMsg struct {
	pair    string
	actions []sync.Action
	dryRun  bool
	err     error
}

type exchangeDoneMsg struct {
	account string
	err     error
}

func (m Model) scanCmd(name string) tea.Cmd {
	cfg, secrets := m.cfg, m.secrets
	pair, _ := cfg.Find(name)
	return func() tea.Msg {
		r, err := engine.Reconcile(context.Background(), cfg, pair, secrets)
		return scannedMsg{pair: name, r: r, err: err}
	}
}

func (m Model) syncCmd(name string, opts sync.Options, only map[string]bool) tea.Cmd {
	cfg, secrets := m.cfg, m.secrets
	pair, _ := cfg.Find(name)
	return func() tea.Msg {
		r, err := engine.Reconcile(context.Background(), cfg, pair, secrets)
		if err != nil {
			return syncedMsg{pair: name, dryRun: opts.DryRun, err: err}
		}
		if len(only) > 0 {
			r.Changes = filterChanges(r.Changes, only)
		}
		actions, err := engine.RunSync(context.Background(), cfg, pair, r, opts)
		return syncedMsg{pair: name, actions: actions, dryRun: opts.DryRun, err: err}
	}
}

func exchangeCmd(flow provider.AuthFlow, code, account string) tea.Cmd {
	return func() tea.Msg {
		return exchangeDoneMsg{account: account, err: flow.Exchange(context.Background(), code)}
	}
}

func (m *Model) state(name string) *pairState {
	ps := m.states[name]
	if ps == nil {
		ps = &pairState{}
		m.states[name] = ps
	}
	return ps
}

func (m Model) selectedPair() (config.Pair, bool) {
	if m.activeTab < 0 || m.activeTab >= len(m.cfg.Pairs) {
		return config.Pair{}, false
	}
	return m.cfg.Pairs[m.activeTab], true
}

// --- Update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case scannedMsg:
		ps := m.state(msg.pair)
		ps.busy = false
		if msg.err != nil {
			ps.err, m.status, m.stErr = msg.err, msg.err.Error(), true
		} else {
			ps.mode, ps.changes, ps.skipped, ps.err = modeDiff, msg.r.Changes, msg.r.Skipped, nil
			ps.rows = orderRows(msg.r.Changes)
			ps.cur = 0
			ps.sel = map[string]bool{}
			m.status, m.stErr = fmt.Sprintf("%s: %d node(s), %d change(s)", msg.pair, len(ps.rows), changedCountC(msg.r.Changes)), false
		}
		return m, nil
	case syncedMsg:
		ps := m.state(msg.pair)
		ps.busy = false
		if msg.err != nil {
			ps.err, m.status, m.stErr = msg.err, msg.err.Error(), true
			return m, nil
		}
		if msg.dryRun {
			ps.mode, ps.actions, ps.err = modePlan, msg.actions, nil
			m.status, m.stErr = msg.pair+": dry-run (nothing changed)", false
			return m, nil
		}
		m.status, m.stErr = msg.pair+": "+summarize(msg.actions), false
		ps.busy, ps.busyWhat = true, "rescanning"
		return m, m.scanCmd(msg.pair) // refresh the diff after a real sync
	case exchangeDoneMsg:
		if msg.err != nil {
			m.status, m.stErr = "login failed: "+msg.err.Error(), true
		} else {
			m.status, m.stErr = fmt.Sprintf("account %q logged in (token in pass)", msg.account), false
			m.overlay = ovNone
		}
		return m, nil
	case tea.KeyMsg:
		if m.overlay != ovNone {
			switch m.overlay {
			case ovPair:
				return m.updatePair(msg)
			case ovProvider:
				return m.updateProvider(msg)
			case ovAccount:
				return m.updateAccount(msg)
			}
		}
		if m.isSettingsTab() {
			return m.updateSettings(msg)
		}
		return m.updateSync(msg)
	}
	return m, nil
}

// tabKeys handles the strip navigation shared by both kinds of tab, wrapping
// around the ends so the tabs cycle.
func (m Model) tabKeys(key string) (Model, tea.Cmd, bool) {
	total := len(m.cfg.Pairs) + 1 // + Settings
	switch key {
	case "right", "l", "tab":
		mm, cmd := m.switchTab((m.activeTab + 1) % total)
		return mm, cmd, true
	case "left", "h", "shift+tab":
		mm, cmd := m.switchTab((m.activeTab - 1 + total) % total)
		return mm, cmd, true
	}
	return m, nil, false
}

func (m Model) updateSync(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "f":
		m.force = !m.force
		m.status, m.stErr = "force "+onoff(m.force)+" (resolves conflicts with up/down)", false
		return m, nil
	case "t":
		m.showSeconds = !m.showSeconds
		m.cfg.View.HideSeconds = !m.showSeconds
		m.dirty = true
		m.status, m.stErr = "seconds "+onoff(m.showSeconds)+" (w to save)", false
		return m, nil
	case "w":
		return m.save(), nil
	case "r":
		if p, ok := m.selectedPair(); ok {
			ps := m.state(p.Name)
			ps.busy, ps.busyWhat = true, "scanning"
			return m, m.scanCmd(p.Name)
		}
		return m, nil
	case "s", "u", "d", "n":
		return m.startSync(key)
	case "a":
		return m.openPair(""), textinput.Blink
	case "e":
		if p, ok := m.selectedPair(); ok {
			return m.openPair(p.Name), textinput.Blink
		}
		return m, nil
	case "x":
		return m.deletePair()
	}
	// tab strip navigation
	if mm, cmd, handled := m.tabKeys(key); handled {
		return mm, cmd
	}
	// file-list navigation within the active pair
	p, ok := m.selectedPair()
	if !ok {
		return m, nil
	}
	ps := m.state(p.Name)
	switch key {
	case "j", "down":
		if ps.cur < len(ps.rows)-1 {
			ps.cur++
		}
	case "k", "up":
		if ps.cur > 0 {
			ps.cur--
		}
	case "g":
		ps.cur = 0
	case "G":
		ps.cur = maxInt(0, len(ps.rows)-1)
	case " ":
		if len(ps.rows) > 0 {
			path := ps.rows[ps.cur].Path
			if ps.sel == nil {
				ps.sel = map[string]bool{}
			}
			ps.sel[path] = !ps.sel[path]
		}
	case "v":
		if ps.sel == nil {
			ps.sel = map[string]bool{}
		}
		allOn := len(ps.rows) > 0
		for _, r := range ps.rows {
			if !ps.sel[r.Path] {
				allOn = false
				break
			}
		}
		for _, r := range ps.rows {
			ps.sel[r.Path] = !allOn
		}
	}
	return m, nil
}

func (m Model) deletePair() (tea.Model, tea.Cmd) {
	p, ok := m.selectedPair()
	if !ok {
		return m, nil
	}
	m.cfg.RemovePair(p.Name)
	delete(m.states, p.Name)
	m.dirty = true
	if m.activeTab >= len(m.cfg.Pairs) && m.activeTab > 0 {
		m.activeTab--
	}
	m.status, m.stErr = "removed "+p.Name+" (w to save)", false
	return m, nil
}

// startSync launches a sync of the selected pair for the given key (s/u/d/n),
// acting on the selected paths when any are marked, otherwise on everything.
func (m Model) startSync(key string) (tea.Model, tea.Cmd) {
	p, ok := m.selectedPair()
	if !ok {
		return m, nil
	}
	opts := sync.Options{Force: m.force}
	what := "syncing"
	switch key {
	case "u":
		opts.Dir = sync.Up
	case "d":
		opts.Dir = sync.Down
	case "n":
		opts.DryRun = true
		what = "dry-run"
	}
	ps := m.state(p.Name)
	only := selectedPaths(ps)
	if len(only) > 0 {
		what += fmt.Sprintf(" %d selected", len(only))
	}
	ps.busy, ps.busyWhat = true, what
	return m, m.syncCmd(p.Name, opts, only)
}

func (m Model) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "q" || key == "ctrl+c" {
		return m, tea.Quit
	}
	if mm, cmd, handled := m.tabKeys(key); handled {
		return mm, cmd
	}
	accts := m.knownAccounts()
	switch key {
	case "j", "down":
		if m.acctCursor < len(accts)-1 {
			m.acctCursor++
		}
	case "k", "up":
		if m.acctCursor > 0 {
			m.acctCursor--
		}
	case "p":
		return m.openProvider("dropbox"), textinput.Blink
	case "A":
		return m.openAccount(), textinput.Blink
	case "e", "enter":
		// Re-login (refresh the token for) the selected account.
		if m.acctCursor < len(accts) {
			return m.openAccountFor(accts[m.acctCursor]), textinput.Blink
		}
	case "x":
		// Remove the selected account's stored token.
		if m.acctCursor < len(accts) {
			name := accts[m.acctCursor]
			if err := m.secrets.Delete(secret.Key("dropbox", name)); err != nil {
				m.status, m.stErr = "remove failed: "+err.Error(), true
			} else {
				m.status, m.stErr = "removed token for "+name, false
				if m.acctCursor > 0 {
					m.acctCursor--
				}
			}
		}
	case "w":
		return m.save(), nil
	}
	return m, nil
}

func (m Model) save() Model {
	if err := config.Save(m.cfg); err != nil {
		m.status, m.stErr = "save failed: "+err.Error(), true
	} else {
		m.dirty = false
		m.status, m.stErr = "saved "+config.Path(), false
	}
	return m
}

// --- pair overlay ---

func (m Model) openPair(name string) Model {
	m.overlay = ovPair
	m.editName = name
	m.focus = 0
	vals := [nFields]string{}
	if name != "" {
		if p, ok := m.cfg.Find(name); ok {
			vals = [nFields]string{p.Name, p.Local, p.Provider, p.Account, p.Remote, strings.Join(p.Ignore, ", ")}
		}
	} else {
		vals[fProvider] = "dropbox"
		vals[fAccount] = "default"
	}
	for i := range m.fields {
		m.fields[i].SetValue(vals[i])
		m.fields[i].Blur()
	}
	m.fields[0].Focus()
	return m
}

func (m Model) updatePair(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.overlay = ovNone
		return m, nil
	case "ctrl+s":
		return m.savePair()
	case "tab", "down":
		m.focus = (m.focus + 1) % nFields
		return m.refocus(), textinput.Blink
	case "shift+tab", "up":
		m.focus = (m.focus - 1 + nFields) % nFields
		return m.refocus(), textinput.Blink
	case "enter":
		if m.focus == nFields-1 {
			return m.savePair()
		}
		m.focus = (m.focus + 1) % nFields
		return m.refocus(), textinput.Blink
	}
	var cmd tea.Cmd
	m.fields[m.focus], cmd = m.fields[m.focus].Update(msg)
	return m, cmd
}

func (m Model) refocus() Model {
	for i := range m.fields {
		if i == m.focus {
			m.fields[i].Focus()
		} else {
			m.fields[i].Blur()
		}
	}
	return m
}

func (m Model) savePair() (tea.Model, tea.Cmd) {
	p := config.Pair{
		Name:     strings.TrimSpace(m.fields[fName].Value()),
		Local:    strings.TrimSpace(m.fields[fLocal].Value()),
		Provider: strings.TrimSpace(m.fields[fProvider].Value()),
		Account:  strings.TrimSpace(m.fields[fAccount].Value()),
		Remote:   strings.TrimSpace(m.fields[fRemote].Value()),
	}
	for _, part := range strings.Split(m.fields[fIgnore].Value(), ",") {
		if s := strings.TrimSpace(part); s != "" {
			p.Ignore = append(p.Ignore, s)
		}
	}
	if p.Name == "" || p.Local == "" || p.Provider == "" || p.Account == "" {
		m.status, m.stErr = "name, local, provider and account are required", true
		return m, nil
	}
	if _, ok := provider.Lookup(p.Provider); !ok {
		m.status, m.stErr = fmt.Sprintf("unknown provider %q (have %s)", p.Provider, strings.Join(provider.Names(), ", ")), true
		return m, nil
	}
	if m.editName != "" && m.editName != p.Name {
		m.cfg.RemovePair(m.editName)
		delete(m.states, m.editName)
	}
	m.cfg.UpsertPair(p)
	delete(m.states, p.Name) // force a fresh scan
	m.dirty = true
	m.overlay = ovNone
	m.status, m.stErr = "pair "+p.Name+" set (w to save, r to scan)", false
	return m, nil
}

// --- provider overlay ---

func (m Model) openProvider(name string) Model {
	m.overlay = ovProvider
	m.provName = name
	m.appKey.SetValue(m.cfg.Settings(name)["app_key"])
	m.appKey.Focus()
	return m
}

func (m Model) updateProvider(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.overlay = ovNone
		return m, nil
	case "enter", "ctrl+s":
		m.cfg.SetProviderSetting(m.provName, "app_key", strings.TrimSpace(m.appKey.Value()))
		m.dirty = true
		m.overlay = ovNone
		m.status, m.stErr = m.provName+" app_key set (w to save)", false
		return m, nil
	}
	var cmd tea.Cmd
	m.appKey, cmd = m.appKey.Update(msg)
	return m, cmd
}

// --- account overlay (interactive OAuth) ---

func (m Model) openAccount() Model { return m.openAccountFor("default") }

// openAccountFor opens the login overlay pre-filled for an account name — used
// both for a fresh account and to re-login (refresh the token of) an existing
// one.
func (m Model) openAccountFor(account string) Model {
	m.overlay = ovAccount
	m.acctStage = 0
	m.flow = nil
	m.authURL = ""
	m.acctProv.SetValue("dropbox")
	m.acctName.SetValue(account)
	m.code.SetValue("")
	m.acctProv.Focus()
	m.acctName.Blur()
	m.focus = 0
	return m
}

func (m Model) updateAccount(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.overlay = ovNone
		return m, nil
	}
	if m.acctStage == 0 {
		switch msg.String() {
		case "tab", "down", "shift+tab", "up":
			m.focus ^= 1
			if m.focus == 0 {
				m.acctProv.Focus()
				m.acctName.Blur()
			} else {
				m.acctProv.Blur()
				m.acctName.Focus()
			}
			return m, textinput.Blink
		case "enter":
			return m.beginAccount()
		}
		var cmd tea.Cmd
		if m.focus == 0 {
			m.acctProv, cmd = m.acctProv.Update(msg)
		} else {
			m.acctName, cmd = m.acctName.Update(msg)
		}
		return m, cmd
	}
	if msg.String() == "enter" {
		code := strings.TrimSpace(m.code.Value())
		if code == "" {
			m.status, m.stErr = "paste the code from the browser first", true
			return m, nil
		}
		m.status, m.stErr = "exchanging code…", false
		return m, exchangeCmd(m.flow, code, m.acctName.Value())
	}
	var cmd tea.Cmd
	m.code, cmd = m.code.Update(msg)
	return m, cmd
}

func (m Model) beginAccount() (tea.Model, tea.Cmd) {
	prov := strings.TrimSpace(m.acctProv.Value())
	acct := strings.TrimSpace(m.acctName.Value())
	if prov == "" || acct == "" {
		m.status, m.stErr = "provider and account name are required", true
		return m, nil
	}
	desc, ok := provider.Lookup(prov)
	if !ok {
		m.status, m.stErr = fmt.Sprintf("unknown provider %q", prov), true
		return m, nil
	}
	flow, err := desc.BeginAuth(m.cfg.Settings(prov), acct, m.secrets)
	if err != nil {
		m.status, m.stErr = err.Error(), true
		return m, nil
	}
	m.flow = flow
	m.authURL = flow.URL()
	m.acctStage = 1
	m.acctProv.Blur()
	m.acctName.Blur()
	m.code.Focus()
	m.status, m.stErr = "", false
	return m, textinput.Blink
}

// --- helpers ---

func summarize(actions []sync.Action) string {
	up, down, del, fail := 0, 0, 0, 0
	for _, a := range actions {
		switch {
		case a.Err != nil:
			fail++
		case strings.HasPrefix(a.Detail, "uploaded"), strings.HasPrefix(a.Detail, "would upload"):
			up++
		case strings.HasPrefix(a.Detail, "downloaded"), strings.HasPrefix(a.Detail, "would download"):
			down++
		case strings.HasPrefix(a.Detail, "deleted"), strings.HasPrefix(a.Detail, "would delete"):
			del++
		}
	}
	s := fmt.Sprintf("↑%d ↓%d ✕%d", up, down, del)
	if fail > 0 {
		s += fmt.Sprintf(" ✗%d", fail)
	}
	return s
}

func onoff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

// orderRows returns the FULL node list — every file and folder in the pair —
// ordered with the differences on top (conflicts, then uploads, then
// downloads) and the in-sync nodes below, each group by path.
func orderRows(changes []sync.Change) []sync.Change {
	out := make([]sync.Change, 0, len(changes))
	for _, c := range changes {
		// Skip ghost rows: an in-sync entry absent on both sides (a stale state
		// key about to be pruned) is not a node to list.
		if c.Kind == sync.InSync && c.Local == nil && c.Remote == nil {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := kindRank(out[i].Kind), kindRank(out[j].Kind); ri != rj {
			return ri < rj
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func kindRank(k sync.Kind) int {
	switch k {
	case sync.Conflict:
		return 0
	case sync.UpNew, sync.UpMod, sync.UpDel:
		return 1
	case sync.DownNew, sync.DownMod, sync.DownDel:
		return 2
	default: // InSync
		return 3
	}
}

func changedCountC(changes []sync.Change) int {
	n := 0
	for _, c := range changes {
		if c.Kind != sync.InSync {
			n++
		}
	}
	return n
}

func selectedPaths(ps *pairState) map[string]bool {
	only := map[string]bool{}
	for p, on := range ps.sel {
		if on {
			only[p] = true
		}
	}
	return only
}

func filterChanges(ch []sync.Change, only map[string]bool) []sync.Change {
	out := make([]sync.Change, 0, len(only))
	for _, c := range ch {
		if only[c.Path] {
			out = append(out, c)
		}
	}
	return out
}

// knownAccounts is the union of accounts referenced by pairs and dropbox tokens
// already stored in pass, sorted.
func (m Model) knownAccounts() []string {
	set := map[string]bool{}
	for _, p := range m.cfg.Pairs {
		if p.Provider == "dropbox" {
			set[p.Account] = true
		}
	}
	if keys, err := m.secrets.Keys(secret.Prefix("dropbox")); err == nil {
		for _, k := range keys {
			if i := strings.LastIndex(k, "/"); i >= 0 {
				set[k[i+1:]] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
