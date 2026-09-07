package diff

import "testing"

func TestHumanSize(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{999, "999 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{4300, "4.2 KiB"},
		{12288, "12 KiB"},
		{1610612736, "1.5 GiB"},
	}
	for _, c := range cases {
		if got := HumanSize(c.n); got != c.want {
			t.Errorf("HumanSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestMiddleTruncate(t *testing.T) {
	if got := middleTruncate("abcdefghij", 5); runeLen(got) != 5 {
		t.Errorf("truncated width = %d, want 5 (%q)", runeLen(got), got)
	}
	if got := middleTruncate("short", 10); got != "short" {
		t.Errorf("should not truncate: %q", got)
	}
}
