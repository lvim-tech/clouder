package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lvim-tech/clouder/internal/provider"
	"github.com/lvim-tech/clouder/internal/state"
)

// maxSingleUpload is Dropbox's simple-upload ceiling; larger files need an
// upload session (Phase 3). Refusing beats a silent 413.
const maxSingleUpload = 150 * 1024 * 1024

// Direction restricts a sync to one way, or neither (Both).
type Direction int

const (
	Both Direction = iota
	Up
	Down
)

// Options shape a run, matching configer's DryRun/Force plus a direction.
type Options struct {
	DryRun bool
	Force  bool
	Dir    Direction
}

// Action is one line of a run's outcome — configer's shape.
type Action struct {
	Name    string // the path
	Detail  string
	Err     error
	Skipped bool
}

// Apply carries out changes, mutating snap as each one succeeds. The caller
// saves snap (once, even after failures — a partial sync must record what
// really happened). In DryRun nothing is performed and snap is left untouched.
func Apply(ctx context.Context, root string, p provider.Provider, snap *state.Snapshot, changes []Change, opts Options) []Action {
	actions := make([]Action, 0, len(changes))
	for _, ch := range changes {
		eff := Effective(ch, opts)

		if eff == Conflict { // only in Both — a direction always picks a winner
			actions = append(actions, Action{
				Name: ch.Path, Skipped: true,
				Detail: "conflict — left alone (sync up or down to pick a side)",
			})
			continue
		}

		if eff == InSync {
			if !opts.DryRun {
				adopt(snap, ch)
			}
			continue // in-sync files are not reported as actions
		}

		if opts.DryRun {
			actions = append(actions, Action{Name: ch.Path, Detail: wouldLabel(eff)})
			continue
		}

		if err := perform(ctx, root, p, snap, ch, eff); err != nil {
			actions = append(actions, Action{Name: ch.Path, Err: err})
			continue
		}
		actions = append(actions, Action{Name: ch.Path, Detail: didLabel(eff)})
	}
	if !opts.DryRun {
		snap.SyncedAt = time.Now()
	}
	return actions
}

// Effective is the operation clouder will perform on a path given the run's
// direction. With no direction (Both) it is the reconciled Kind, so conflicts
// stay conflicts. With Up or Down it is a MIRROR: the chosen side wins outright,
// so `down` on a local-only file deletes it (remote is the truth and has none),
// and `up` on a remote-only file deletes it. Mirror never yields a conflict.
func Effective(ch Change, opts Options) Kind {
	L, R := ch.Local != nil, ch.Remote != nil
	switch opts.Dir {
	case Up:
		switch {
		case L && R:
			if sameSide(ch.Local, ch.Remote) {
				return InSync
			}
			return UpMod
		case L && !R:
			return UpNew
		case !L && R:
			return UpDel // remote extra → delete it to match local
		default:
			return InSync
		}
	case Down:
		switch {
		case L && R:
			if sameSide(ch.Local, ch.Remote) {
				return InSync
			}
			return DownMod
		case !L && R:
			return DownNew
		case L && !R:
			return DownDel // local extra → delete it to match remote
		default:
			return InSync
		}
	default: // Both
		return ch.Kind
	}
}

func sameSide(a, b *Side) bool { return a.Hash == b.Hash && a.Size == b.Size }

func perform(ctx context.Context, root string, p provider.Provider, snap *state.Snapshot, ch Change, eff Kind) error {
	rel := ch.Path
	if strings.HasSuffix(rel, "/") {
		return performDir(ctx, root, p, snap, rel, eff)
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	switch eff {
	case UpNew, UpMod:
		info, err := os.Stat(abs)
		if err != nil {
			return err
		}
		if info.Size() > maxSingleUpload {
			return fmt.Errorf("larger than 150 MB — upload sessions not yet implemented")
		}
		f, err := os.Open(abs)
		if err != nil {
			return err
		}
		defer f.Close()
		ent, err := p.Upload(ctx, rel, f, info.Size(), info.ModTime())
		if err != nil {
			return err
		}
		snap.Entries[rel] = state.Entry{Hash: ent.Hash, Size: ent.Size}

	case DownNew, DownMod:
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(abs), ".clouder-*.tmp")
		if err != nil {
			return err
		}
		name := tmp.Name()
		if err := p.Download(ctx, rel, tmp); err != nil {
			tmp.Close()
			os.Remove(name)
			return err
		}
		if err := tmp.Close(); err != nil {
			os.Remove(name)
			return err
		}
		if err := os.Rename(name, abs); err != nil {
			os.Remove(name)
			return err
		}
		var e state.Entry
		if ch.Remote != nil {
			e = state.Entry{Hash: ch.Remote.Hash, Size: ch.Remote.Size}
			// Stamp the local file with the remote's modification time, so a
			// downloaded copy reads as the same age as its source instead of
			// "just now" — the two sides then show the same time when in sync.
			if !ch.Remote.Modified.IsZero() {
				os.Chtimes(abs, ch.Remote.Modified, ch.Remote.Modified)
			}
		}
		snap.Entries[rel] = e

	case UpDel:
		if err := p.Delete(ctx, rel); err != nil && !errors.Is(err, provider.ErrNotFound) {
			return err
		}
		delete(snap.Entries, rel)

	case DownDel:
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return err
		}
		delete(snap.Entries, rel)
	}
	return nil
}

// performDir handles a directory entry (a trailing-slash path): create or
// remove an empty folder instead of transferring bytes.
func performDir(ctx context.Context, root string, p provider.Provider, snap *state.Snapshot, rel string, eff Kind) error {
	name := strings.TrimSuffix(rel, "/")
	abs := filepath.Join(root, filepath.FromSlash(name))
	switch eff {
	case UpNew, UpMod:
		if err := p.Mkdir(ctx, name); err != nil {
			return err
		}
		snap.Entries[rel] = state.Entry{Hash: provider.DirHash}
	case DownNew, DownMod:
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return err
		}
		snap.Entries[rel] = state.Entry{Hash: provider.DirHash}
	case UpDel:
		if err := p.Delete(ctx, name); err != nil && !errors.Is(err, provider.ErrNotFound) {
			return err
		}
		delete(snap.Entries, rel)
	case DownDel:
		// Only removes an empty directory; if files have since appeared, leave
		// it — a non-empty-dir error here is not a failure of the sync.
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			if !isDirNotEmpty(err) {
				return err
			}
		}
		delete(snap.Entries, rel)
	}
	return nil
}

func isDirNotEmpty(err error) bool {
	return strings.Contains(err.Error(), "directory not empty") || strings.Contains(err.Error(), "not empty")
}

// adopt records an in-sync path (or prunes it when gone from both sides), so
// the next run has an accurate baseline.
func adopt(snap *state.Snapshot, ch Change) {
	switch {
	case ch.Local == nil && ch.Remote == nil:
		delete(snap.Entries, ch.Path)
	case ch.Local != nil:
		snap.Entries[ch.Path] = state.Entry{Hash: ch.Local.Hash, Size: ch.Local.Size}
	case ch.Remote != nil:
		snap.Entries[ch.Path] = state.Entry{Hash: ch.Remote.Hash, Size: ch.Remote.Size}
	}
}

func wouldLabel(k Kind) string {
	switch k {
	case UpNew:
		return "would upload (new)"
	case UpMod:
		return "would upload"
	case UpDel:
		return "would delete (remote)"
	case DownNew:
		return "would download (new)"
	case DownMod:
		return "would download"
	case DownDel:
		return "would delete (local)"
	}
	return ""
}

func didLabel(k Kind) string {
	switch k {
	case UpNew:
		return "uploaded (new)"
	case UpMod:
		return "uploaded"
	case UpDel:
		return "deleted (remote)"
	case DownNew:
		return "downloaded (new)"
	case DownMod:
		return "downloaded"
	case DownDel:
		return "deleted (local)"
	}
	return ""
}

// Tally counts changes by kind, for summary lines.
type Tally map[Kind]int

// Count buckets a change set.
func Count(changes []Change) Tally {
	t := Tally{}
	for _, c := range changes {
		t[c.Kind]++
	}
	return t
}

// DeleteShare returns how many of the changes would delete a file under the
// run's direction, and that as a fraction of the nodes in the set — the
// delete-storm guard reads this to refuse a run that would wipe most of a side
// from a mis-mounted or emptied folder. It counts EFFECTIVE deletes, so a
// mirror `down` that would erase many local-only files is caught too.
func DeleteShare(changes []Change, opts Options) (dels int, frac float64) {
	total := 0
	for _, c := range changes {
		if c.Kind != InSync {
			total++
		}
		if k := Effective(c, opts); k == UpDel || k == DownDel {
			dels++
		}
	}
	if total > 0 {
		frac = float64(dels) / float64(total)
	}
	return dels, frac
}
