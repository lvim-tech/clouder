package sync

import (
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/lvim-tech/clouder/internal/config"
	"github.com/lvim-tech/clouder/internal/provider"
)

// Scan walks the local root and returns rel-path → Side, hashing each regular
// file with the provider's own content hash so it compares to a remote listing
// without a download. Ignored paths are pruned; symlinks and other non-regular
// files are skipped and named in the returned slice (never followed, never
// uploaded — the same "adopts whatever it points at" caution configer applies).
func Scan(root string, pair config.Pair, p provider.Provider) (map[string]Side, []string, error) {
	out := map[string]Side{}
	var skipped []string
	dirMtime := map[string]time.Time{} // rel dir → mtime
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if pair.Ignored(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if info, ierr := d.Info(); ierr == nil {
				dirMtime[rel] = info.ModTime()
			}
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if !info.Mode().IsRegular() { // symlinks, sockets, devices, fifos
			skipped = append(skipped, rel)
			return nil
		}
		h, herr := p.HashLocal(path)
		if herr != nil {
			return herr
		}
		out[rel] = Side{Hash: h, Size: info.Size(), Modified: info.ModTime()}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// Record empty (leaf) directories so a folder with nothing in it still
	// syncs. A directory is a leaf-empty one when nothing — no scanned file and
	// no other directory — has it as a prefix; creating those recreates every
	// empty branch, and non-empty branches are recreated by their own files.
	for dir, mtime := range dirMtime {
		prefix := dir + "/"
		empty := true
		for f := range out {
			if strings.HasPrefix(f, prefix) {
				empty = false
				break
			}
		}
		if empty {
			for other := range dirMtime {
				if other != dir && strings.HasPrefix(other, prefix) {
					empty = false
					break
				}
			}
		}
		if empty {
			out[dir+"/"] = Side{Hash: provider.DirHash, Modified: mtime}
		}
	}
	return out, skipped, nil
}

// RemoteMap turns a provider listing into the map Reconcile wants.
func RemoteMap(entries []provider.Entry) map[string]Side {
	m := make(map[string]Side, len(entries))
	for _, e := range entries {
		m[e.Path] = Side{Hash: e.Hash, Size: e.Size, Modified: e.Modified}
	}
	return m
}
