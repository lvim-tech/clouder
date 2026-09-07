package dropbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lvim-tech/clouder/internal/provider"
	"github.com/lvim-tech/clouder/internal/secret"
)

func init() {
	provider.Register(provider.Descriptor{
		Name:      "dropbox",
		Open:      open,
		BeginAuth: beginAuth,
	})
}

// dbx is one authenticated account bound to one remote root folder.
type dbx struct {
	c    *client
	root string // "" for the account root, or "/folder" (no trailing slash)
}

func open(settings map[string]string, account, root string, secrets secret.Store) (provider.Provider, error) {
	appKey := strings.TrimSpace(settings["app_key"])
	if appKey == "" {
		return nil, fmt.Errorf("no app_key in [providers.dropbox]")
	}
	refresh, err := secrets.Get(secret.Key("dropbox", account))
	if err != nil {
		return nil, fmt.Errorf("no stored token for account %q — run clouder --add-account dropbox %s: %w", account, account, err)
	}
	return &dbx{c: newClient(appKey, refresh), root: normalizeRoot(root)}, nil
}

func normalizeRoot(root string) string {
	r := strings.TrimSpace(root)
	if r == "" || r == "/" {
		return ""
	}
	if !strings.HasPrefix(r, "/") {
		r = "/" + r
	}
	return strings.TrimRight(r, "/")
}

// full builds the absolute Dropbox path for a relative file path.
func (d *dbx) full(rel string) string {
	return d.root + "/" + strings.TrimPrefix(rel, "/")
}

func (d *dbx) HashLocal(path string) (string, error) { return HashLocal(path) }

type meta struct {
	Tag            string `json:".tag"`
	PathDisplay    string `json:"path_display"`
	Size           int64  `json:"size"`
	ContentHash    string `json:"content_hash"`
	ServerModified string `json:"server_modified"`
	ClientModified string `json:"client_modified"`
	Rev            string `json:"rev"`
}

func (m meta) entry(root string) provider.Entry {
	rel := m.PathDisplay
	prefix := strings.TrimRight(root, "/")
	if prefix != "" && len(rel) >= len(prefix) && strings.EqualFold(rel[:len(prefix)], prefix) {
		rel = rel[len(prefix):]
	}
	rel = strings.TrimPrefix(rel, "/")
	// Prefer client_modified — the file's own last-edit time — over
	// server_modified, which is merely when Dropbox received the upload.
	ts := m.ClientModified
	if ts == "" {
		ts = m.ServerModified
	}
	t, _ := time.Parse(time.RFC3339, ts)
	return provider.Entry{Path: rel, Size: m.Size, Hash: m.ContentHash, Rev: m.Rev, Modified: t}
}

func (d *dbx) List(ctx context.Context) ([]provider.Entry, error) {
	var files []provider.Entry
	var folders []string // relative folder paths
	var resp struct {
		Entries []meta `json:"entries"`
		Cursor  string `json:"cursor"`
		HasMore bool   `json:"has_more"`
	}
	// Dropbox wants "" (not "/") for the account root.
	start := map[string]any{"path": d.root, "recursive": true, "limit": 2000}
	if err := d.c.rpc(ctx, "/files/list_folder", start, &resp); err != nil {
		return nil, err
	}
	for {
		for _, m := range resp.Entries {
			switch m.Tag {
			case "file":
				files = append(files, m.entry(d.root))
			case "folder":
				folders = append(folders, m.entry(d.root).Path)
			}
		}
		if !resp.HasMore {
			break
		}
		cursor := resp.Cursor
		resp.Entries, resp.Cursor, resp.HasMore = nil, "", false
		if err := d.c.rpc(ctx, "/files/list_folder/continue", map[string]any{"cursor": cursor}, &resp); err != nil {
			return nil, err
		}
	}

	// Emit only the *empty* folders as directory entries — a folder that holds
	// files (or non-empty subfolders) is recreated when those are, so recording
	// it too would be redundant. A folder is empty when nothing has it as a
	// prefix.
	out := files
	for _, f := range folders {
		prefix := f + "/"
		empty := true
		for _, e := range files {
			if strings.HasPrefix(e.Path, prefix) {
				empty = false
				break
			}
		}
		if empty {
			for _, g := range folders {
				if g != f && strings.HasPrefix(g, prefix) {
					empty = false
					break
				}
			}
		}
		if empty {
			out = append(out, provider.Entry{Path: f + "/", Hash: provider.DirHash})
		}
	}
	return out, nil
}

func (d *dbx) Mkdir(ctx context.Context, rel string) error {
	err := d.c.rpc(ctx, "/files/create_folder_v2", map[string]any{"path": d.full(rel), "autorename": false}, nil)
	if err != nil {
		// An already-existing folder comes back as a 409 conflict — that is the
		// desired end state, so treat it as success.
		if ae, ok := err.(*apiError); ok && ae.status == http.StatusConflict && strings.Contains(ae.body, "conflict") {
			return nil
		}
		return err
	}
	return nil
}

func (d *dbx) Download(ctx context.Context, rel string, w io.Writer) error {
	arg, _ := json.Marshal(map[string]string{"path": d.full(rel)})
	resp, err := d.c.send(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, contentBase+"/files/download", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Dropbox-API-Arg", headerSafe(arg))
		return req, nil
	})
	if err != nil {
		if isNotFound(err) {
			return provider.ErrNotFound
		}
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(w, resp.Body)
	return err
}

func (d *dbx) Upload(ctx context.Context, rel string, r io.Reader, size int64, modTime time.Time) (provider.Entry, error) {
	argMap := map[string]any{
		"path": d.full(rel), "mode": "overwrite", "autorename": false, "mute": true,
	}
	// Preserve the file's edit time as Dropbox's client_modified. Dropbox wants
	// UTC seconds and rejects a future time, so clamp to now.
	if !modTime.IsZero() {
		if modTime.After(time.Now()) {
			modTime = time.Now()
		}
		argMap["client_modified"] = modTime.UTC().Format("2006-01-02T15:04:05Z")
	}
	arg, _ := json.Marshal(argMap)
	resp, err := d.c.send(ctx, func() (*http.Request, error) {
		// Seek back to the start on a retry, so a refreshed 401 re-sends the
		// whole file rather than a truncated tail.
		if s, ok := r.(io.Seeker); ok {
			if _, serr := s.Seek(0, io.SeekStart); serr != nil {
				return nil, serr
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, contentBase+"/files/upload", r)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Dropbox-API-Arg", headerSafe(arg))
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = size
		return req, nil
	})
	if err != nil {
		return provider.Entry{}, err
	}
	defer resp.Body.Close()
	var m meta
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return provider.Entry{}, err
	}
	return m.entry(d.root), nil
}

func (d *dbx) Delete(ctx context.Context, rel string) error {
	err := d.c.rpc(ctx, "/files/delete_v2", map[string]string{"path": d.full(rel)}, nil)
	if err != nil {
		if isNotFound(err) {
			return provider.ErrNotFound
		}
		return err
	}
	return nil
}

// isNotFound recognizes Dropbox's path/not_found error, which arrives as a 409
// with a tagged body.
func isNotFound(err error) bool {
	var ae *apiError
	if e, ok := err.(*apiError); ok {
		ae = e
	}
	if ae == nil {
		return false
	}
	return ae.status == http.StatusConflict && strings.Contains(ae.body, "not_found")
}
