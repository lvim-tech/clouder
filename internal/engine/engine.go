// Package engine is the shared orchestration both the CLI and the TUI drive:
// open a pair's provider, list the remote, scan the local folder, reconcile,
// and (for a sync) apply and persist. Keeping it here means the interface and
// the command line cannot drift apart.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/lvim-tech/clouder/internal/config"
	"github.com/lvim-tech/clouder/internal/provider"
	"github.com/lvim-tech/clouder/internal/secret"
	"github.com/lvim-tech/clouder/internal/state"
	"github.com/lvim-tech/clouder/internal/sync"
)

// Reconciled is everything a diff or a sync needs about one pair, computed once.
type Reconciled struct {
	Changes  []sync.Change
	Skipped  []string
	Provider provider.Provider
	Snap     state.Snapshot
}

// Reconcile opens the pair, lists the remote, scans the local folder and
// classifies every path.
func Reconcile(ctx context.Context, cfg config.Config, pair config.Pair, secrets secret.Store) (Reconciled, error) {
	desc, ok := provider.Lookup(pair.Provider)
	if !ok {
		return Reconciled{}, fmt.Errorf("unknown provider %q", pair.Provider)
	}
	prov, err := desc.Open(cfg.Settings(pair.Provider), pair.Account, pair.Remote, secrets)
	if err != nil {
		return Reconciled{}, err
	}
	snap, err := state.Load(pair.Name)
	if err != nil {
		return Reconciled{}, err
	}
	if snap.Provider != "" && (snap.Provider != pair.Provider || snap.Account != pair.Account) {
		return Reconciled{}, fmt.Errorf(
			"state for pair %q was built against %s/%s but config now says %s/%s — delete %s to re-baseline",
			pair.Name, snap.Provider, snap.Account, pair.Provider, pair.Account, state.Path(pair.Name))
	}
	remote, err := prov.List(ctx)
	if err != nil {
		return Reconciled{}, fmt.Errorf("list remote: %w", err)
	}
	local, skipped, err := sync.Scan(config.Expand(pair.Local), pair, prov)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Reconciled{}, fmt.Errorf("local folder %s does not exist", config.Expand(pair.Local))
		}
		return Reconciled{}, fmt.Errorf("scan local: %w", err)
	}
	return Reconciled{
		Changes:  sync.Reconcile(snap.Entries, local, sync.RemoteMap(remote)),
		Skipped:  skipped,
		Provider: prov,
		Snap:     snap,
	}, nil
}

// ErrDeleteStorm is returned when a sync would remove too much at once — the
// mis-mounted/emptied-folder guard. Force overrides it.
type ErrDeleteStorm struct {
	Deletes int
	Frac    float64
}

func (e ErrDeleteStorm) Error() string {
	return fmt.Sprintf("would delete %d files (%.0f%% of the pair) — refusing; use force if intended", e.Deletes, e.Frac*100)
}

// RunSync applies the reconciled changes and, unless DryRun, saves the updated
// snapshot. It enforces the delete-storm guard first.
func RunSync(ctx context.Context, cfg config.Config, pair config.Pair, r Reconciled, opts sync.Options) ([]sync.Action, error) {
	if dels, frac := sync.DeleteShare(r.Changes, opts); !opts.Force && dels > 10 && frac > 0.25 {
		return nil, ErrDeleteStorm{Deletes: dels, Frac: frac}
	}
	snap := r.Snap
	actions := sync.Apply(ctx, config.Expand(pair.Local), r.Provider, &snap, r.Changes, opts)
	if !opts.DryRun {
		snap.Provider, snap.Account = pair.Provider, pair.Account
		if err := snap.Save(); err != nil {
			return actions, fmt.Errorf("save state: %w", err)
		}
	}
	return actions, nil
}
