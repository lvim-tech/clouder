// clouder keeps a local folder and a cloud folder in step, both ways: the two
// are equal peers, and a state file from the last sync is what tells a change
// on one side from a change on the other.
//
//	clouder                      the two-column diff for every pair
//	clouder --diff [pair]        the two-column diff: local | remote
//	clouder --inventory [pair]   what state every file is in, compactly
//	clouder --sync [pair]        make both sides agree
//	clouder --add-account <provider> [account]   log in, store the token in pass
//	clouder --init               write a starting config.toml
//
// --dry-run reports without touching anything. --up / --down restrict a sync to
// one direction; a conflict — changed on both sides, or changed on one and
// deleted on the other — is never resolved silently: it is skipped and shown,
// until --up or --down together with --force says whose version wins.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/lvim-tech/clouder/internal/config"
	"github.com/lvim-tech/clouder/internal/diff"
	"github.com/lvim-tech/clouder/internal/provider"
	_ "github.com/lvim-tech/clouder/internal/provider/dropbox" // register backend
	"github.com/lvim-tech/clouder/internal/secret"
	"github.com/lvim-tech/clouder/internal/state"
	"github.com/lvim-tech/clouder/internal/sync"
	"github.com/lvim-tech/clouder/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "clouder:", err)
		os.Exit(1)
	}
}

func run() error {
	var verb string
	var pos []string
	var opts sync.Options
	var up, down bool

	for _, a := range os.Args[1:] {
		switch {
		case a == "--dry-run" || a == "-n":
			opts.DryRun = true
		case a == "--force":
			opts.Force = true
		case a == "--up":
			up = true
		case a == "--down":
			down = true
		case verb == "":
			verb = a
		// A late dashed token is a misspelt flag, not a positional — the same
		// trap configer grew after `--forc` silently became an app name.
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q", a)
		default:
			pos = append(pos, a)
		}
	}
	if up && down {
		return fmt.Errorf("--up and --down are opposites; pick one")
	}
	switch {
	case up:
		opts.Dir = sync.Up
	case down:
		opts.Dir = sync.Down
	}

	switch verb {
	case "--init":
		return initConfig()
	case "--add-account":
		return addAccount(pos)
	case "":
		return runTUI()
	case "--diff":
		return showDiff(pos, opts)
	case "--inventory", "-i":
		return showInventory(pos)
	case "--sync":
		return runSync(pos, opts)
	default:
		return fmt.Errorf("unknown argument %q (--diff, --inventory, --sync, --add-account, --init)", verb)
	}
}

// pairsToRun resolves an optional single pair name to the set of pairs to act
// on: all of them when none is named.
func pairsToRun(cfg config.Config, pos []string) ([]config.Pair, error) {
	if len(pos) > 1 {
		return nil, fmt.Errorf("too many arguments (%q): one pair at a time", pos[1])
	}
	if len(cfg.Pairs) == 0 {
		return nil, fmt.Errorf("no pairs configured — run `clouder --init` and edit %s", config.Path())
	}
	if len(pos) == 1 {
		p, ok := cfg.Find(pos[0])
		if !ok {
			return nil, fmt.Errorf("no pair %q in %s", pos[0], config.Path())
		}
		return []config.Pair{p}, nil
	}
	return cfg.Pairs, nil
}

// reconciled opens the provider, lists the remote, scans the local folder and
// returns the classified changes plus the loaded snapshot.
func reconciled(ctx context.Context, cfg config.Config, pair config.Pair) ([]sync.Change, provider.Provider, state.Snapshot, []string, error) {
	desc, ok := provider.Lookup(pair.Provider)
	if !ok {
		return nil, nil, state.Snapshot{}, nil, fmt.Errorf("unknown provider %q", pair.Provider)
	}
	prov, err := desc.Open(cfg.Settings(pair.Provider), pair.Account, pair.Remote, secret.Pass{})
	if err != nil {
		return nil, nil, state.Snapshot{}, nil, err
	}
	snap, err := state.Load(pair.Name)
	if err != nil {
		return nil, nil, state.Snapshot{}, nil, err
	}
	if snap.Provider != "" && (snap.Provider != pair.Provider || snap.Account != pair.Account) {
		return nil, nil, state.Snapshot{}, nil, fmt.Errorf(
			"state for pair %q was built against %s/%s but config now says %s/%s — delete %s to re-baseline",
			pair.Name, snap.Provider, snap.Account, pair.Provider, pair.Account, state.Path(pair.Name))
	}

	remote, err := prov.List(ctx)
	if err != nil {
		return nil, nil, state.Snapshot{}, nil, fmt.Errorf("list remote: %w", err)
	}
	local, skipped, err := sync.Scan(config.Expand(pair.Local), pair, prov)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, state.Snapshot{}, nil, fmt.Errorf("local folder %s does not exist", config.Expand(pair.Local))
		}
		return nil, nil, state.Snapshot{}, nil, fmt.Errorf("scan local: %w", err)
	}
	changes := sync.Reconcile(snap.Entries, local, sync.RemoteMap(remote))
	return changes, prov, snap, skipped, nil
}

func header(pair config.Pair) string {
	return fmt.Sprintf("%s  (%s  ⇄  %s:%s:%s)",
		pair.Name, config.Expand(pair.Local), pair.Provider, pair.Account, remoteLabel(pair.Remote))
}

func remoteLabel(r string) string {
	if r == "" {
		return "/"
	}
	return r
}

func showDiff(pos []string, opts sync.Options) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pairs, err := pairsToRun(cfg, pos)
	if err != nil {
		return err
	}
	ctx := context.Background()
	width := termWidth()
	color := isTTY()
	for _, pair := range pairs {
		changes, _, _, skipped, err := reconciled(ctx, cfg, pair)
		if err != nil {
			fmt.Printf("%s\n   ✗ %v\n\n", pair.Name, err)
			continue
		}
		changes = filterView(changes, opts.Dir)
		// Show the `=` rows only for a small pair; a big already-synced folder
		// should not bury its few changes under hundreds of equal lines.
		showInSync := countChanged(changes) <= 40
		for _, line := range diff.Render(header(pair), changes, width, showInSync, color) {
			fmt.Println(line)
		}
		for _, s := range skipped {
			fmt.Printf("   (skipped non-regular file: %s)\n", s)
		}
		fmt.Println()
	}
	return nil
}

// filterView keeps in-sync and conflict rows, plus only the chosen direction's
// changes when a direction was given (so --diff --up previews an upward sync).
func filterView(changes []sync.Change, dir sync.Direction) []sync.Change {
	if dir == sync.Both {
		return changes
	}
	out := changes[:0:0]
	for _, c := range changes {
		keep := c.Kind == sync.InSync || c.Kind == sync.Conflict ||
			(dir == sync.Up && isUpKind(c.Kind)) ||
			(dir == sync.Down && isDownKind(c.Kind))
		if keep {
			out = append(out, c)
		}
	}
	return out
}

func showInventory(pos []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pairs, err := pairsToRun(cfg, pos)
	if err != nil {
		return err
	}
	ctx := context.Background()
	total := map[sync.Kind]int{}
	for _, pair := range pairs {
		changes, _, _, _, err := reconciled(ctx, cfg, pair)
		if err != nil {
			fmt.Printf("%s\n   ✗ %v\n\n", pair.Name, err)
			continue
		}
		fmt.Println(header(pair))
		shown := 0
		for _, c := range changes {
			total[c.Kind]++
			if c.Kind == sync.InSync {
				continue
			}
			shown++
			fmt.Printf("   %-2s %s\n", invMark(c.Kind), c.Path)
		}
		if shown == 0 {
			fmt.Println("   — everything in sync")
		}
		fmt.Println()
	}
	fmt.Println(diff.SummaryLine(flatten(total)))
	return nil
}

func runSync(pos []string, opts sync.Options) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pairs, err := pairsToRun(cfg, pos)
	if err != nil {
		return err
	}
	ctx := context.Background()
	failed := 0
	for _, pair := range pairs {
		changes, prov, snap, _, err := reconciled(ctx, cfg, pair)
		if err != nil {
			fmt.Printf("%s\n   ✗ %v\n\n", pair.Name, err)
			failed++
			continue
		}
		// Delete-storm guard: a mis-mounted or emptied local folder would
		// reconcile as "delete everything remote". Refuse without --force.
		if dels, frac := sync.DeleteShare(changes, opts); !opts.Force && dels > 10 && frac > 0.25 {
			fmt.Printf("%s\n   ✗ would delete %d files (%.0f%% of the pair) — refusing; re-run with --force if intended\n\n",
				pair.Name, dels, frac*100)
			failed++
			continue
		}

		actions := sync.Apply(ctx, config.Expand(pair.Local), prov, &snap, changes, opts)
		fmt.Println(pair.Name)
		printed := false
		for _, a := range actions {
			printed = true
			switch {
			case a.Err != nil:
				failed++
				fmt.Printf("   ✗ %-28s %v\n", a.Name, a.Err)
			case a.Skipped:
				fmt.Printf("   · %-28s %s\n", a.Name, a.Detail)
			default:
				fmt.Printf("   • %-28s %s\n", a.Name, a.Detail)
			}
		}
		if !printed {
			fmt.Println("   — nothing to do")
		}
		if !opts.DryRun {
			snap.Provider, snap.Account = pair.Provider, pair.Account
			if err := snap.Save(); err != nil {
				fmt.Printf("   ✗ save state: %v\n", err)
				failed++
			}
		}
		fmt.Println()
		fmt.Println(syncSummary(changes, opts))
		fmt.Println()
	}
	if failed > 0 {
		return fmt.Errorf("%d item(s) failed", failed)
	}
	return nil
}

func syncSummary(changes []sync.Change, opts sync.Options) string {
	t := sync.Count(changes)
	up := t[sync.UpNew] + t[sync.UpMod]
	down := t[sync.DownNew] + t[sync.DownMod]
	del := t[sync.UpDel] + t[sync.DownDel]
	line := fmt.Sprintf("↑ uploaded %d   ↓ downloaded %d   ✕ deleted %d   = in sync %d   ! conflicts %d",
		up, down, del, t[sync.InSync], t[sync.Conflict])
	if t[sync.Conflict] > 0 && !(opts.Force && opts.Dir != sync.Both) {
		line += "\nconflicts left alone — resolve with --up --force or --down --force"
	}
	return line
}

func addAccount(pos []string) error {
	if len(pos) < 1 {
		return fmt.Errorf("usage: clouder --add-account <provider> [account]")
	}
	if len(pos) > 2 {
		return fmt.Errorf("too many arguments (%q)", pos[2])
	}
	name := pos[0]
	account := "default"
	if len(pos) == 2 {
		account = pos[1]
	}
	desc, ok := provider.Lookup(name)
	if !ok {
		return fmt.Errorf("unknown provider %q (have %s)", name, strings.Join(provider.Names(), ", "))
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	flow, err := desc.BeginAuth(cfg.Settings(name), account, secret.Pass{})
	if err != nil {
		return err
	}
	fmt.Println("Open this URL, approve, and paste the code shown by Dropbox:")
	fmt.Println()
	fmt.Println("  " + flow.URL())
	fmt.Println()
	fmt.Print("code: ")
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return fmt.Errorf("no code entered")
	}
	if err := flow.Exchange(context.Background(), sc.Text()); err != nil {
		return err
	}
	fmt.Printf("account %q ready for %s\n", account, name)
	return nil
}

// runTUI opens the interactive settings interface. It works with an empty
// config too — that is how a first-time user adds their first pair and account
// and writes the file out.
func runTUI() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	return tui.Run(cfg, secret.Pass{})
}

func initConfig() error {
	dir, path := config.Dir(), config.Path()
	if dir == "" || path == "" {
		return fmt.Errorf("no home directory: set HOME")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// O_EXCL: the check and the write are one syscall, so an existing config
	// cannot be clobbered by landing between them.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s already exists", path)
		}
		return err
	}
	if _, err := f.WriteString(starter); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", path)
	return nil
}

// --- small helpers ---

func isUpKind(k sync.Kind) bool {
	return k == sync.UpNew || k == sync.UpMod || k == sync.UpDel
}
func isDownKind(k sync.Kind) bool {
	return k == sync.DownNew || k == sync.DownMod || k == sync.DownDel
}

func countChanged(changes []sync.Change) int {
	n := 0
	for _, c := range changes {
		if c.Kind != sync.InSync {
			n++
		}
	}
	return n
}

func invMark(k sync.Kind) string {
	switch k {
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
	return "="
}

// flatten turns a count map into a synthetic change slice so SummaryLine can
// render it without a second formatter.
func flatten(counts map[sync.Kind]int) []sync.Change {
	var out []sync.Change
	for k, n := range counts {
		for i := 0; i < n; i++ {
			out = append(out, sync.Change{Kind: k})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}
