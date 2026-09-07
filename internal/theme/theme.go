// Package theme is clouder's palette as plain data — no rendering library — so
// it stays the same lvim-colorscheme format the rest of this desktop uses. A
// theme is a built-in base plus a file in ~/.config/clouder/themes/<name>.yaml
// (the very files lvim-colorscheme generates for configer), selected by
// [theme] name in config.toml. Only the roles a file repeats are overridden.
package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Color is one palette entry, adapting to the terminal background.
type Color struct {
	Light string `yaml:"light,omitempty"`
	Dark  string `yaml:"dark,omitempty"`
}

func (c Color) IsZero() bool { return c.Light == "" && c.Dark == "" }

// UnmarshalYAML accepts both `accent: "#rrggbb"` and `accent: {light:…, dark:…}`.
func (c *Color) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var v string
		if err := node.Decode(&v); err != nil {
			return err
		}
		c.Light, c.Dark = v, v
		return nil
	}
	type raw Color
	var r raw
	if err := node.Decode(&r); err != nil {
		return err
	}
	*c = Color(r)
	if c.Light == "" {
		c.Light = c.Dark
	}
	if c.Dark == "" {
		c.Dark = c.Light
	}
	return nil
}

// ColorSet is the nine roles the interface is drawn from.
type ColorSet struct {
	Accent    Color `yaml:"accent,omitempty"`
	AccentAlt Color `yaml:"accent_alt,omitempty"`
	Text      Color `yaml:"text,omitempty"`
	Muted     Color `yaml:"muted,omitempty"`
	Subtle    Color `yaml:"subtle,omitempty"`
	Success   Color `yaml:"success,omitempty"`
	Warning   Color `yaml:"warning,omitempty"`
	Error     Color `yaml:"error,omitempty"`
	TitleFg   Color `yaml:"title_fg,omitempty"`
	// Extra named hues from the lvim-colorscheme palette, beyond configer's
	// nine roles — clouder uses them to colour the two diff columns.
	Cyan   Color `yaml:"cyan,omitempty"`
	Yellow Color `yaml:"yellow,omitempty"`
}

// Theme is a name plus its palette.
type Theme struct {
	Name   string   `yaml:"name,omitempty"`
	Colors ColorSet `yaml:"colors,omitempty"`
}

// builtin presets. "default" is the clipack/Nord palette so clouder matches the
// rest of the desktop out of the box; "mono" uses the terminal's own colours.
var builtin = map[string]Theme{
	"default": {
		Name: "default",
		Colors: ColorSet{
			Accent:    Color{Light: "#2f5d9e", Dark: "#81a1c1"},
			AccentAlt: Color{Light: "#a2542b", Dark: "#d08770"},
			Text:      Color{Light: "#1c1c1c", Dark: "#e5e9f0"},
			Muted:     Color{Light: "#6b6b6b", Dark: "#7f8896"},
			Subtle:    Color{Light: "#c8c8c8", Dark: "#3b4252"},
			Success:   Color{Light: "#217a3d", Dark: "#a3be8c"},
			Warning:   Color{Light: "#b3261e", Dark: "#bf616a"},
			Error:     Color{Light: "#b3261e", Dark: "#bf616a"},
			TitleFg:   Color{Light: "#ffffff", Dark: "#1c1c1c"},
			Cyan:      Color{Light: "#3a7a7a", Dark: "#88c0d0"},
			Yellow:    Color{Light: "#8a6d00", Dark: "#ebcb8b"},
		},
	},
	"mono": {Name: "mono"},
}

// Default is the fully populated fallback.
func Default() Theme { return builtin["default"] }

// ThemesDir is ~/.config/clouder/themes.
func ThemesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("no home directory")
	}
	return filepath.Join(home, ".config", "clouder", "themes"), nil
}

// Names lists built-ins plus installed theme files, sorted.
func Names() []string {
	set := map[string]bool{}
	for n := range builtin {
		set[n] = true
	}
	if dir, err := ThemesDir(); err == nil {
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				ext := filepath.Ext(e.Name())
				if ext == ".yaml" || ext == ".yml" {
					set[e.Name()[:len(e.Name())-len(ext)]] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// loadFile reads a theme file from the themes directory.
func loadFile(name string) (Theme, error) {
	dir, err := ThemesDir()
	if err != nil {
		return Theme{}, err
	}
	if name != filepath.Base(name) || name == "." || name == ".." {
		return Theme{}, fmt.Errorf("invalid theme name %q", name)
	}
	for _, ext := range []string{".yaml", ".yml"} {
		data, err := os.ReadFile(filepath.Join(dir, name+ext))
		if err != nil {
			continue
		}
		var t Theme
		if err := yaml.Unmarshal(data, &t); err != nil {
			return Theme{}, fmt.Errorf("parsing theme %s%s: %w", name, ext, err)
		}
		t.Name = name
		return t, nil
	}
	return Theme{}, fmt.Errorf("theme %q not found", name)
}

// Resolve returns the palette for name: the default preset merged with a
// built-in or a themes-directory file. An unknown name falls back to default
// rather than failing — a missing theme should not stop the interface opening.
func Resolve(name string) Theme {
	base := Default()
	switch name {
	case "", "default":
		return base
	case "mono":
		return builtin["mono"]
	}
	over, err := loadFile(name)
	if err != nil {
		if b, ok := builtin[name]; ok {
			return merge(base, b)
		}
		return base
	}
	return merge(base, over)
}

func merge(base, over Theme) Theme {
	out := base
	if over.Name != "" {
		out.Name = over.Name
	}
	set := func(dst *Color, src Color) {
		if !src.IsZero() {
			*dst = src
		}
	}
	c := &out.Colors
	o := over.Colors
	set(&c.Accent, o.Accent)
	set(&c.AccentAlt, o.AccentAlt)
	set(&c.Text, o.Text)
	set(&c.Muted, o.Muted)
	set(&c.Subtle, o.Subtle)
	set(&c.Success, o.Success)
	set(&c.Warning, o.Warning)
	set(&c.Error, o.Error)
	set(&c.TitleFg, o.TitleFg)
	set(&c.Cyan, o.Cyan)
	set(&c.Yellow, o.Yellow)
	return out
}
