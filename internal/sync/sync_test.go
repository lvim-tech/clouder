package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lvim-tech/clouder/internal/provider"
	"github.com/lvim-tech/clouder/internal/state"
)

func hh(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

type fakeProv struct {
	files map[string][]byte
	dirs  map[string]bool
}

func newFake() *fakeProv {
	return &fakeProv{files: map[string][]byte{}, dirs: map[string]bool{}}
}

func (f *fakeProv) Mkdir(_ context.Context, rel string) error { f.dirs[rel] = true; return nil }

func (f *fakeProv) List(context.Context) ([]provider.Entry, error) {
	var out []provider.Entry
	for k, v := range f.files {
		out = append(out, provider.Entry{Path: k, Size: int64(len(v)), Hash: hh(v)})
	}
	return out, nil
}
func (f *fakeProv) Download(_ context.Context, rel string, w io.Writer) error {
	b, ok := f.files[rel]
	if !ok {
		return provider.ErrNotFound
	}
	_, err := w.Write(b)
	return err
}
func (f *fakeProv) Upload(_ context.Context, rel string, r io.Reader, _ int64, _ time.Time) (provider.Entry, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return provider.Entry{}, err
	}
	f.files[rel] = b
	return provider.Entry{Path: rel, Size: int64(len(b)), Hash: hh(b)}, nil
}
func (f *fakeProv) Delete(_ context.Context, rel string) error { delete(f.files, rel); return nil }
func (f *fakeProv) HashLocal(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return hh(b), nil
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestApplyUpload(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "hello")
	f := newFake()
	snap := state.Snapshot{Entries: map[string]state.Entry{}}
	changes := []Change{{Path: "a.txt", Kind: UpNew, Local: &Side{Hash: hh([]byte("hello")), Size: 5}}}
	Apply(context.Background(), root, f, &snap, changes, Options{})
	if string(f.files["a.txt"]) != "hello" {
		t.Fatalf("remote not written: %q", f.files["a.txt"])
	}
	if snap.Entries["a.txt"].Hash != hh([]byte("hello")) {
		t.Fatalf("state not updated")
	}
}

func TestApplyDownload(t *testing.T) {
	root := t.TempDir()
	f := newFake()
	f.files["b/c.txt"] = []byte("world")
	snap := state.Snapshot{Entries: map[string]state.Entry{}}
	changes := []Change{{Path: "b/c.txt", Kind: DownNew, Remote: &Side{Hash: hh([]byte("world")), Size: 5}}}
	Apply(context.Background(), root, f, &snap, changes, Options{})
	got, err := os.ReadFile(filepath.Join(root, "b", "c.txt"))
	if err != nil || string(got) != "world" {
		t.Fatalf("local not written: %v %q", err, got)
	}
	if snap.Entries["b/c.txt"].Hash != hh([]byte("world")) {
		t.Fatalf("state not updated")
	}
}

func TestApplyDeletes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "local-only.txt", "x")
	f := newFake()
	f.files["remote-only.txt"] = []byte("y")
	snap := state.Snapshot{Entries: map[string]state.Entry{
		"remote-only.txt": {Hash: hh([]byte("y")), Size: 1},
		"local-only.txt":  {Hash: hh([]byte("x")), Size: 1},
	}}
	changes := []Change{
		{Path: "remote-only.txt", Kind: UpDel},                       // delete remote
		{Path: "local-only.txt", Kind: DownDel, Local: &Side{Size: 1}}, // delete local
	}
	Apply(context.Background(), root, f, &snap, changes, Options{})
	if _, ok := f.files["remote-only.txt"]; ok {
		t.Fatal("remote file not deleted")
	}
	if _, err := os.Stat(filepath.Join(root, "local-only.txt")); !os.IsNotExist(err) {
		t.Fatal("local file not deleted")
	}
	if len(snap.Entries) != 0 {
		t.Fatalf("state not pruned: %v", snap.Entries)
	}
}

func TestApplyConflictSkippedThenForced(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "c.txt", "local-version")
	f := newFake()
	f.files["c.txt"] = []byte("remote-version")
	ch := []Change{{Path: "c.txt", Kind: Conflict,
		Local: &Side{Hash: hh([]byte("local-version")), Size: 13}, Remote: &Side{Hash: hh([]byte("remote-version")), Size: 14}}}

	// No direction: skipped, nothing moves.
	snap := state.Snapshot{Entries: map[string]state.Entry{}}
	acts := Apply(context.Background(), root, f, &snap, ch, Options{})
	if len(acts) != 1 || !acts[0].Skipped {
		t.Fatalf("conflict not skipped: %+v", acts)
	}
	if string(f.files["c.txt"]) != "remote-version" {
		t.Fatalf("remote changed on a skipped conflict")
	}

	// --up --force: local wins, uploaded.
	snap2 := state.Snapshot{Entries: map[string]state.Entry{}}
	Apply(context.Background(), root, f, &snap2, ch, Options{Force: true, Dir: Up})
	if string(f.files["c.txt"]) != "local-version" {
		t.Fatalf("force-up did not upload local: %q", f.files["c.txt"])
	}
}

func TestApplyEmptyDir(t *testing.T) {
	root := t.TempDir()
	f := newFake()
	snap := state.Snapshot{Entries: map[string]state.Entry{}}

	// Upload a new empty local folder.
	up := []Change{{Path: "photos/2024/", Kind: UpNew, Local: &Side{Hash: provider.DirHash}}}
	Apply(context.Background(), root, f, &snap, up, Options{})
	if !f.dirs["photos/2024"] {
		t.Fatalf("empty dir not created remotely: %v", f.dirs)
	}
	if snap.Entries["photos/2024/"].Hash != provider.DirHash {
		t.Fatalf("dir not recorded in state")
	}

	// Download a new empty remote folder.
	down := []Change{{Path: "inbox/", Kind: DownNew, Remote: &Side{Hash: provider.DirHash}}}
	Apply(context.Background(), root, f, &snap, down, Options{})
	if fi, err := os.Stat(filepath.Join(root, "inbox")); err != nil || !fi.IsDir() {
		t.Fatalf("empty dir not created locally: %v", err)
	}
}

func TestApplyDryRunTouchesNothing(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "hello")
	f := newFake()
	snap := state.Snapshot{Entries: map[string]state.Entry{}}
	ch := []Change{{Path: "a.txt", Kind: UpNew, Local: &Side{Hash: hh([]byte("hello")), Size: 5}}}
	acts := Apply(context.Background(), root, f, &snap, ch, Options{DryRun: true})
	if len(acts) != 1 || acts[0].Detail == "" {
		t.Fatalf("dry-run action missing: %+v", acts)
	}
	if len(f.files) != 0 || len(snap.Entries) != 0 {
		t.Fatal("dry-run mutated something")
	}
}
