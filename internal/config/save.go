package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// Save writes the config back to config.toml atomically. It is what the TUI
// calls after editing pairs or provider settings. Marshalling does not preserve
// the starter file's teaching comments — a short header note is prepended so
// the rewritten file is still recognizable.
func Save(cfg Config) error {
	dir, path := Dir(), Path()
	if dir == "" || path == "" {
		return fmt.Errorf("no home directory: set HOME")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	data := append([]byte("# clouder config — edited via the TUI (hand-editing is fine too).\n\n"), body...)

	tmp, err := os.CreateTemp(dir, "config.*.tmp")
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
	return os.Rename(name, path)
}

// SetProviderSetting records key=value under [providers.<provider>], creating
// the maps as needed.
func (c *Config) SetProviderSetting(provider, key, value string) {
	if c.Providers == nil {
		c.Providers = map[string]map[string]string{}
	}
	if c.Providers[provider] == nil {
		c.Providers[provider] = map[string]string{}
	}
	c.Providers[provider][key] = value
}

// UpsertPair adds pair p, or replaces the existing one with the same name.
func (c *Config) UpsertPair(p Pair) {
	for i := range c.Pairs {
		if c.Pairs[i].Name == p.Name {
			c.Pairs[i] = p
			return
		}
	}
	c.Pairs = append(c.Pairs, p)
}

// RemovePair drops the pair named name, reporting whether it existed.
func (c *Config) RemovePair(name string) bool {
	for i := range c.Pairs {
		if c.Pairs[i].Name == name {
			c.Pairs = append(c.Pairs[:i], c.Pairs[i+1:]...)
			return true
		}
	}
	return false
}

// AbsLocal expands a possibly-relative local path for display or checks.
func (p Pair) AbsLocal() string { return Expand(p.Local) }
