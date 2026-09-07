package sync

import (
	"testing"

	"github.com/lvim-tech/clouder/internal/state"
)

func sd(hash string, size int64) Side { return Side{Hash: hash, Size: size} }

func TestReconcileTable(t *testing.T) {
	const p = "f"
	type tc struct {
		name   string
		st     map[string]state.Entry
		local  map[string]Side
		remote map[string]Side
		want   Kind
	}
	st := func(h string, s int64) map[string]state.Entry {
		return map[string]state.Entry{p: {Hash: h, Size: s}}
	}
	lm := func(sd Side) map[string]Side { return map[string]Side{p: sd} }

	cases := []tc{
		{"2 UpNew", nil, lm(sd("h1", 10)), nil, UpNew},
		{"3 DownNew", nil, nil, lm(sd("h1", 10)), DownNew},
		{"4a adopt InSync", nil, lm(sd("h1", 10)), lm(sd("h1", 10)), InSync},
		{"4b conflict new/new", nil, lm(sd("h1", 10)), lm(sd("h2", 10)), Conflict},
		{"5a InSync", st("h1", 10), lm(sd("h1", 10)), lm(sd("h1", 10)), InSync},
		{"5b UpMod", st("h1", 10), lm(sd("h2", 12)), lm(sd("h1", 10)), UpMod},
		{"5c DownMod", st("h1", 10), lm(sd("h1", 10)), lm(sd("h2", 12)), DownMod},
		{"5d converge InSync", st("h1", 10), lm(sd("h2", 12)), lm(sd("h2", 12)), InSync},
		{"5e conflict both-edited", st("h1", 10), lm(sd("h2", 12)), lm(sd("h3", 13)), Conflict},
		{"6a UpDel", st("h1", 10), nil, lm(sd("h1", 10)), UpDel},
		{"6b conflict del-vs-edit", st("h1", 10), nil, lm(sd("h2", 12)), Conflict},
		{"7a DownDel", st("h1", 10), lm(sd("h1", 10)), nil, DownDel},
		{"7b conflict edit-vs-del", st("h1", 10), lm(sd("h2", 12)), nil, Conflict},
		{"8 both gone InSync", st("h1", 10), nil, nil, InSync},
		{"corrupt hash= size≠", nil, lm(sd("h1", 10)), lm(sd("h1", 11)), Conflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Reconcile(c.st, c.local, c.remote)
			if len(got) != 1 {
				t.Fatalf("want 1 change, got %d", len(got))
			}
			if got[0].Kind != c.want {
				t.Fatalf("kind = %v, want %v", got[0].Kind, c.want)
			}
			if got[0].Path != p {
				t.Fatalf("path = %q", got[0].Path)
			}
		})
	}
}

func TestReconcileEmpty(t *testing.T) {
	if got := Reconcile(nil, nil, nil); len(got) != 0 {
		t.Fatalf("want empty, got %d", len(got))
	}
}

func TestReconcileSorted(t *testing.T) {
	local := map[string]Side{"b": sd("h", 1), "a": sd("h", 1), "c": sd("h", 1)}
	got := Reconcile(nil, local, nil)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len %d", len(got))
	}
	for i := range want {
		if got[i].Path != want[i] {
			t.Fatalf("order[%d]=%q want %q", i, got[i].Path, want[i])
		}
	}
}
