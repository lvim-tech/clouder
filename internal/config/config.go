// Package config loads ~/.config/clouder/config.toml: the pairs of
// (local folder ⇄ provider folder) clouder keeps in step, and the per-provider
// settings. It mirrors configer's Load discipline — a missing file is a normal
// first run, an unreadable one is a hard error — and the same __HOME__/~
// expansion, so a config synced through configer stays portable.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lvim-tech/clouder/internal/provider"
	"github.com/pelletier/go-toml/v2"
)

// Config is the whole file.
type Config struct {
	Pairs     []Pair                       `toml:"pair"`
	Providers map[string]map[string]string `toml:"providers"`
	Theme     ThemeSel                     `toml:"theme"`
	View      ViewOpts                     `toml:"view"`
}

// ThemeSel selects the interface palette by name (a built-in, or a file in
// ~/.config/clouder/themes/). Empty means the default.
type ThemeSel struct {
	Name string `toml:"name"`
}

// Config also carries view preferences.
type ViewOpts struct {
	// HideSeconds drops the :ss from timestamps (default is to show them).
	HideSeconds bool `toml:"hide_seconds"`
}

// Pair is one local⇄remote relationship.
type Pair struct {
	Name     string   `toml:"name"`
	Local    string   `toml:"local"`
	Provider string   `toml:"provider"`
	Account  string   `toml:"account"`
	Remote   string   `toml:"remote"`
	Ignore   []string `toml:"ignore"`
}

// DefaultIgnores are dropped from every pair. Smaller than configer's list —
// this is user data, not a config tree — but the usual editor and VCS litter.
var DefaultIgnores = []string{
	".git", "*.bak", "*.bak-*", "*.swp", "*.swo", "*~", ".DS_Store", "Thumbs.db",
}

// Dir is ~/.config/clouder.
func Dir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "clouder")
}

// Path is ~/.config/clouder/config.toml.
func Path() string {
	d := Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "config.toml")
}

// Load reads and validates the config. A missing file yields an empty Config.
func Load() (Config, error) {
	var cfg Config
	p := Path()
	if p == "" {
		return cfg, fmt.Errorf("no home directory: set HOME")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // first run
		}
		return cfg, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", p, err)
	}
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	seen := map[string]bool{}
	for i, pr := range c.Pairs {
		if pr.Name == "" {
			return fmt.Errorf("pair #%d: name required", i+1)
		}
		if seen[pr.Name] {
			return fmt.Errorf("duplicate pair %q", pr.Name)
		}
		seen[pr.Name] = true
		if pr.Local == "" {
			return fmt.Errorf("pair %q: local required", pr.Name)
		}
		if pr.Provider == "" {
			return fmt.Errorf("pair %q: provider required", pr.Name)
		}
		if _, ok := provider.Lookup(pr.Provider); !ok {
			have := strings.Join(provider.Names(), ", ")
			if have == "" {
				have = "none registered"
			}
			return fmt.Errorf("pair %q: unknown provider %q (have %s)", pr.Name, pr.Provider, have)
		}
		if pr.Account == "" {
			return fmt.Errorf("pair %q: account required", pr.Name)
		}
	}
	return nil
}

// Find returns the pair by name.
func (c Config) Find(name string) (Pair, bool) {
	for _, p := range c.Pairs {
		if p.Name == name {
			return p, true
		}
	}
	return Pair{}, false
}

// Settings returns the [providers.<name>] table, never nil.
func (c Config) Settings(prov string) map[string]string {
	if s := c.Providers[prov]; s != nil {
		return s
	}
	return map[string]string{}
}

// Expand resolves a leading __HOME__ or ~ against the user's own home. Like
// configer, it leaves ~otheruser alone.
func Expand(p string) string {
	if p == "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	switch {
	case strings.HasPrefix(p, "__HOME__/"):
		return filepath.Join(home, p[len("__HOME__/"):])
	case p == "__HOME__":
		return home
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	}
	return p
}

// Ignored reports whether a slash-separated relative path is filtered out, by
// DefaultIgnores plus the pair's own patterns. A pattern matches the basename,
// the whole relative path, or any single path segment — so a directory name
// listed anywhere kills the whole subtree beneath it.
func (p Pair) Ignored(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}
	segs := strings.Split(rel, "/")
	pats := make([]string, 0, len(DefaultIgnores)+len(p.Ignore))
	pats = append(pats, DefaultIgnores...)
	pats = append(pats, p.Ignore...)
	for _, pat := range pats {
		if ok, _ := filepath.Match(pat, base); ok {
			return true
		}
		if ok, _ := filepath.Match(pat, rel); ok {
			return true
		}
		for _, s := range segs {
			if ok, _ := filepath.Match(pat, s); ok {
				return true
			}
		}
	}
	return false
}
