// Package sync is clouder's engine: reconcile decides what to do, Apply does it.
// reconcile.go is a pure function — no I/O — so the whole decision table is
// unit-testable without a network or a disk.
package sync

import (
	"sort"
	"time"

	"github.com/lvim-tech/clouder/internal/state"
)

// Kind is the outcome for one path.
type Kind int

const (
	InSync  Kind = iota // equal, or both deleted (state pruned)
	UpNew               // created locally → upload
	UpMod               // edited locally → upload
	UpDel               // deleted locally → delete the remote copy
	DownNew             // created remotely → download
	DownMod             // edited remotely → download
	DownDel             // deleted remotely → delete the local copy
	Conflict            // both sides moved, or change-vs-delete
)

func (k Kind) String() string {
	switch k {
	case InSync:
		return "in sync"
	case UpNew:
		return "up (new)"
	case UpMod:
		return "up (modified)"
	case UpDel:
		return "up (delete remote)"
	case DownNew:
		return "down (new)"
	case DownMod:
		return "down (modified)"
	case DownDel:
		return "down (delete local)"
	case Conflict:
		return "conflict"
	}
	return "?"
}

// Side is one peer's view of a path. A nil *Side means the file is absent there.
type Side struct {
	Hash     string
	Size     int64
	Modified time.Time
}

// Change is the engine's verdict for one path, carrying both peers' current
// state and the previous recorded state, so the diff view and Apply have
// everything without re-reading.
type Change struct {
	Path   string
	Kind   Kind
	Local  *Side        // nil when absent locally
	Remote *Side        // nil when absent remotely
	Prev   *state.Entry // nil when not in state
}

// Reconcile compares the recorded state with the current local and remote
// listings and classifies every path. Output is sorted by path for stable,
// testable results.
func Reconcile(st map[string]state.Entry, local, remote map[string]Side) []Change {
	keys := map[string]struct{}{}
	for k := range st {
		keys[k] = struct{}{}
	}
	for k := range local {
		keys[k] = struct{}{}
	}
	for k := range remote {
		keys[k] = struct{}{}
	}
	paths := make([]string, 0, len(keys))
	for k := range keys {
		paths = append(paths, k)
	}
	sort.Strings(paths)

	out := make([]Change, 0, len(paths))
	for _, p := range paths {
		s, inS := st[p]
		l, inL := local[p]
		r, inR := remote[p]

		ch := Change{Path: p}
		if inL {
			v := l
			ch.Local = &v
		}
		if inR {
			v := r
			ch.Remote = &v
		}
		if inS {
			v := s
			ch.Prev = &v
		}
		ch.Kind = classify(inS, inL, inR, s, l, r)
		out = append(out, ch)
	}
	return out
}

func classify(inS, inL, inR bool, s state.Entry, l, r Side) Kind {
	switch {
	case !inL && !inR:
		return InSync // gone on both sides → prune the state entry

	case inL && !inR:
		if !inS {
			return UpNew
		}
		if l.Hash == s.Hash {
			return DownDel // remote deleted, local untouched → delete local
		}
		return Conflict // local changed, remote deleted

	case !inL && inR:
		if !inS {
			return DownNew
		}
		if r.Hash == s.Hash {
			return UpDel // local deleted, remote untouched → delete remote
		}
		return Conflict // remote changed, local deleted

	default: // present on both sides
		if !inS {
			if equal(l, r) {
				return InSync // adopt: same content, never synced before
			}
			return Conflict
		}
		lChanged := l.Hash != s.Hash
		rChanged := r.Hash != s.Hash
		switch {
		case !lChanged && !rChanged:
			return InSync
		case lChanged && !rChanged:
			return UpMod
		case !lChanged && rChanged:
			return DownMod
		default: // both changed
			if equal(l, r) {
				return InSync // converged on identical content
			}
			return Conflict
		}
	}
}

// equal treats two live sides as the same file only when hash and size agree;
// a matching hash with a mismatched size is corrupt state, and the caller sees
// it as a conflict rather than a silent no-op.
func equal(a, b Side) bool {
	return a.Hash == b.Hash && a.Size == b.Size
}
