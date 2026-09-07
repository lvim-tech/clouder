// Package state persists the snapshot of the last successful sync: for each
// path, the provider content hash and size that were true on both sides. It is
// what turns "these two files differ" into "which side moved" — without it a
// two-way sync cannot tell a local edit from a remote delete.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry is one path's recorded state.
type Entry struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// Snapshot is the whole state file for one pair.
type Snapshot struct {
	Version  int              `json:"version"`
	Pair     string           `json:"pair"`
	Provider string           `json:"provider"`
	Account  string           `json:"account"`
	SyncedAt time.Time        `json:"synced_at"`
	Entries  map[string]Entry `json:"entries"`
}

// Dir is ${XDG_STATE_HOME:-~/.local/state}/clouder.
func Dir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "clouder")
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".local", "state", "clouder")
}

// Path is the state file for one pair.
func Path(pair string) string {
	return filepath.Join(Dir(), pair+".json")
}

// Load reads the snapshot for pair. A missing file is an empty snapshot — the
// first-run case, where identical files on both sides adopt without transfer.
func Load(pair string) (Snapshot, error) {
	s := Snapshot{Version: 1, Pair: pair, Entries: map[string]Entry{}}
	data, err := os.ReadFile(Path(pair))
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("%s: %w", Path(pair), err)
	}
	if s.Entries == nil {
		s.Entries = map[string]Entry{}
	}
	return s, nil
}

// Save writes the snapshot atomically: a temp file in the same directory, then
// rename. A snapshot half-written because the disk filled is worse than one not
// written at all.
func (s Snapshot) Save() error {
	dir := Dir()
	if dir == "" {
		return fmt.Errorf("no home directory: set HOME")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, s.Pair+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, Path(s.Pair))
}
