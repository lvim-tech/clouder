package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpand(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := map[string]string{
		"__HOME__/Documents": filepath.Join(home, "Documents"),
		"~/Pictures":         filepath.Join(home, "Pictures"),
		"~":                  home,
		"/abs/path":          "/abs/path",
		"relative":           "relative",
	}
	for in, want := range cases {
		if got := Expand(in); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIgnored(t *testing.T) {
	p := Pair{Ignore: []string{"node_modules", "*.log"}}
	yes := []string{
		".git/config",           // default: .git subtree
		"a/b/.git/HEAD",         // .git anywhere in the path
		"draft.swp",             // default *.swp
		"node_modules/x/y.js",   // custom dir subtree
		"logs/app.log",          // custom *.log
		".DS_Store",             // default basename
	}
	no := []string{"notes.md", "src/main.go", "gitignore.txt", "logfile"}
	for _, rel := range yes {
		if !p.Ignored(rel) {
			t.Errorf("Ignored(%q) = false, want true", rel)
		}
	}
	for _, rel := range no {
		if p.Ignored(rel) {
			t.Errorf("Ignored(%q) = true, want false", rel)
		}
	}
}
