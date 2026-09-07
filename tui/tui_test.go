package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lvim-tech/clouder/internal/config"
	_ "github.com/lvim-tech/clouder/internal/provider/dropbox" // register for Lookup
)

type fakeSecrets struct{ m map[string]string }

func (f *fakeSecrets) Get(k string) (string, error) {
	v, ok := f.m[k]
	if !ok {
		return "", fmt.Errorf("no secret %s", k)
	}
	return v, nil
}
func (f *fakeSecrets) Set(k, v string) error { f.m[k] = v; return nil }
func (f *fakeSecrets) Delete(k string) error { delete(f.m, k); return nil }
func (f *fakeSecrets) Keys(prefix string) ([]string, error) {
	var out []string
	for k := range f.m {
		if strings.HasPrefix(k, prefix+"/") {
			out = append(out, k)
		}
	}
	return out, nil
}

func newModel() Model {
	cfg := config.Config{Pairs: []config.Pair{
		{Name: "notes", Local: "~/n", Provider: "dropbox", Account: "default", Remote: "/notes"},
	}}
	return New(cfg, &fakeSecrets{m: map[string]string{}})
}

func TestHomeView(t *testing.T) {
	v := newModel().View()
	// The pair shows as a tab, alongside the Settings tab and the title.
	if !strings.Contains(v, "notes") || !strings.Contains(v, "Settings") || !strings.Contains(v, "clouder") {
		t.Fatalf("home view missing content:\n%s", v)
	}
}

func TestProviderAndAccountViews(t *testing.T) {
	m := newModel().openProvider("dropbox")
	if !strings.Contains(m.View(), "app_key") {
		t.Fatalf("provider view missing app_key:\n%s", m.View())
	}
	a := newModel().openAccount()
	av := a.View()
	if !strings.Contains(av, "provider") || !strings.Contains(av, "account") {
		t.Fatalf("account view missing fields:\n%s", av)
	}
}

func TestSavePair(t *testing.T) {
	m := newModel().openPair("")
	m.fields[fName].SetValue("photos")
	m.fields[fLocal].SetValue("~/Pictures")
	// provider/account are prefilled by openPair (dropbox/default)
	mm, _ := m.savePair()
	m2 := mm.(Model)
	if _, ok := m2.cfg.Find("photos"); !ok {
		t.Fatalf("pair not added")
	}
	if !m2.dirty {
		t.Fatalf("dirty flag not set")
	}
}

func TestSavePairRequiresFields(t *testing.T) {
	m := newModel().openPair("")
	m.fields[fName].SetValue("") // missing name
	m.fields[fLocal].SetValue("~/x")
	mm, _ := m.savePair()
	m2 := mm.(Model)
	if !m2.stErr {
		t.Fatalf("expected validation error for missing name")
	}
}

func TestBeginAccount(t *testing.T) {
	// Without an app_key, BeginAuth must fail cleanly.
	m := newModel().openAccount()
	mm, _ := m.beginAccount()
	if !mm.(Model).stErr {
		t.Fatalf("expected error without app_key")
	}

	// With an app_key, it advances to the code stage with a real URL.
	m2 := newModel()
	m2.cfg.SetProviderSetting("dropbox", "app_key", "TESTKEY")
	m2 = m2.openAccount()
	mm2, _ := m2.beginAccount()
	got := mm2.(Model)
	if got.acctStage != 1 {
		t.Fatalf("stage = %d, want 1", got.acctStage)
	}
	if !strings.Contains(got.authURL, "client_id=TESTKEY") || !strings.Contains(got.authURL, "code_challenge") {
		t.Fatalf("bad auth URL: %s", got.authURL)
	}
}
