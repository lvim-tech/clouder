// Package provider is the seam every cloud backend plugs into. The core engine
// speaks only to the Provider interface; a backend registers a Descriptor from
// its own init(), and a blank import in main.go pulls it into the binary. Add
// MEGA later as internal/provider/mega with its own init() — nothing here
// changes.
package provider

import (
	"context"
	"errors"
	"io"
	"sort"
	"time"

	"github.com/lvim-tech/clouder/internal/secret"
)

// Entry is one remote file as the provider reports it.
type Entry struct {
	Path     string    // relative to the pair's remote root, slash-separated
	Size     int64     // bytes
	Hash     string    // provider content hash, lowercase hex
	Rev      string    // provider revision id, "" when the provider has none
	Modified time.Time // server-side mtime — display only, never a change signal
}

// Provider is one authenticated account bound to one remote folder.
type Provider interface {
	// List returns every file under the remote root, recursively.
	List(ctx context.Context) ([]Entry, error)
	// Download streams the file at rel into w.
	Download(ctx context.Context, rel string, w io.Writer) error
	// Upload creates or replaces the file at rel from r, returning its new
	// metadata (whose Hash is what the engine records as authoritative). modTime
	// is the local file's last-edit time, preserved as the remote's
	// client-modified time so the edit time survives the round trip.
	Upload(ctx context.Context, rel string, r io.Reader, size int64, modTime time.Time) (Entry, error)
	// Delete removes the file (or folder) at rel. A missing path is not an
	// error (ErrNotFound may be returned, and the engine treats it as done).
	Delete(ctx context.Context, rel string) error
	// Mkdir creates the (empty) folder at rel, creating parents as needed. An
	// already-existing folder is not an error.
	Mkdir(ctx context.Context, rel string) error
	// HashLocal computes THIS provider's content hash of a local file, so a
	// local and a remote file compare without a download.
	HashLocal(path string) (string, error)
}

// AuthFlow is an interactive login split into its two driveable steps, so the
// same flow serves the CLI (print URL, read code from stdin) and the TUI (show
// URL, take the code in a text field).
type AuthFlow interface {
	// URL is the address the user opens to approve access.
	URL() string
	// Exchange trades the code the user pasted for a long-lived credential and
	// stores it through the secret store.
	Exchange(ctx context.Context, code string) error
}

// Descriptor is what a backend registers.
type Descriptor struct {
	Name string
	// Open binds settings + account + remote root into a ready Provider.
	Open func(settings map[string]string, account, root string, secrets secret.Store) (Provider, error)
	// BeginAuth starts an interactive login for an account. The caller shows
	// flow.URL(), collects the code, and calls flow.Exchange.
	BeginAuth func(settings map[string]string, account string, secrets secret.Store) (AuthFlow, error)
}

// ErrNotFound lets Download/Delete report a missing remote path so the engine
// can tell "gone" from "failed".
var ErrNotFound = errors.New("not found")

// DirHash is the sentinel "content hash" of a directory entry, so an empty
// folder can travel through the same reconcile as a file: its hash never
// changes, so it only ever reads as present or absent, never modified. A
// directory's path key carries a trailing slash.
const DirHash = "\x00clouder-dir"

var registry = map[string]Descriptor{}

// Register records a backend. It panics on an empty or duplicate name: both are
// programmer errors caught at startup.
func Register(d Descriptor) {
	if d.Name == "" {
		panic("provider: empty name")
	}
	if _, dup := registry[d.Name]; dup {
		panic("provider: duplicate registration " + d.Name)
	}
	registry[d.Name] = d
}

// Lookup returns the descriptor for name.
func Lookup(name string) (Descriptor, bool) {
	d, ok := registry[name]
	return d, ok
}

// Names lists registered providers, sorted, for error messages.
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
