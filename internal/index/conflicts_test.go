package index

import "testing"

func TestConflictSurvivorPath(t *testing.T) {
	cases := []struct {
		rel  string
		want string
	}{
		// The trash restore writer: name.conflict-YYYYMMDDTHHMMSS.ext.
		{"proj/a.conflict-20260923T121212.md", "proj/a.md"},
		{"a.conflict-20260923T121212.md", "a.md"},
		{"a.conflict-20260923T121212.html", "a.html"},
		// The HTML save writer: a dash in the timestamp, and -2, -3…
		// when even the conflict name was taken.
		{"dash.conflict-20260923-121212.html", "dash.html"},
		{"dash.conflict-20260923-121212-2.html", "dash.html"},
		{"dash.conflict-20260923-121212-3.htm", "dash.htm"},
		{"deep/dir/name.conflict-20260101T000000.markdown", "deep/dir/name.markdown"},
		// Not conflicts.
		{"a.md", ""},
		{"a.conflict.md", ""},
		{"notes on conflict-20260923T121212.md", ""},
		{"a.conflict-20260923T1212.md", ""},
		{"a.conflict-20260923121.md", ""},
	}
	for _, c := range cases {
		if got := ConflictSurvivorPath(c.rel); got != c.want {
			t.Errorf("ConflictSurvivorPath(%q) = %q, want %q", c.rel, got, c.want)
		}
	}
}
