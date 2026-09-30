package server

import (
	"net/url"
	"strings"
	"testing"
)

// searchHitTitles pulls the titles of the note hits out of a response.
func searchHitTitles(t *testing.T, e *env, query string) []string {
	t.Helper()
	var res struct {
		Mode string
		Hits []struct {
			Note struct {
				Title string
				Path  string
			}
		}
	}
	if code := e.get(t, "/api/search?q="+url.QueryEscape(query), &res); code != 200 {
		t.Fatalf("%s: status %d", query, code)
	}
	out := make([]string, 0, len(res.Hits))
	for _, h := range res.Hits {
		out = append(out, h.Note.Title)
	}
	return out
}

func TestSearchOperatorsEndpoint(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		q    string
		want string
	}{
		{"tag:greeting", "Hello"},
		{"#greeting", "Hello"},
		{"#GREETING", "Hello"},
		{"tag:greeting -tag:greeting", ""},
		{"is:html", "Dash"},
		{"has:image", "Hello"},
		{"has:attachment", "Hello"},
		{"is:task", ""},
		{"path:home/sub", "Second"},
		{"path:home/", "Hello,Second"},
		{"space:work", "Dash"},
		{"space:work is:html", "Dash"},
		{"space:home is:html", ""},
		{"is:untagged", "Dash,Loose,Second"},
		{"author:alice", ""},
		{"before:2000-01-01", ""},
		{"after:2000-01-01", "Dash,Hello,Loose,Second"},
		{"foo:bar", ""},       // unknown operator is searched as text
		{"greeting", "Hello"}, // plain text still works
		{"tag:greeting greeting", "Hello"},
	}
	for _, tc := range cases {
		got := searchHitTitles(t, e, tc.q)
		// Titles are unordered except where one hit is expected; join
		// for the multi-hit cases and compare as sets by sorting both.
		key := strings.Join(got, ",")
		if key != tc.want && !sameSet(got, strings.Split(tc.want, ",")) {
			t.Errorf("%s: got [%s], want [%s]", tc.q, key, tc.want)
		}
	}
}

func sameSet(a []string, b []string) bool {
	if len(a) != len(b) || (len(b) == 1 && b[0] == "") {
		return len(a) == 0 && len(b) == 1
	}
	seen := map[string]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		if x == "" {
			return false
		}
		seen[x]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestSearchAfterBeforeDates(t *testing.T) {
	e := newEnv(t)
	// Everything in the fixture is newer than 2000; the operator bound
	// is by modified time, and files were written "a minute ago".
	if got := searchHitTitles(t, e, "after:2000-01-01 -is:untagged"); len(got) != 1 || got[0] != "Hello" {
		t.Fatalf("mixed operators: %v", got)
	}
}

func TestRegexSearchEndpointOperators(t *testing.T) {
	e := newEnv(t)
	if !e.srv.Ripgrep.Available() {
		t.Skip("rg not installed; regex passthrough untested")
	}
	var rx struct {
		Mode string
		Hits []struct {
			Path, Text, ID, Title string
			Line                  int
		}
	}
	if code := e.get(t, "/api/search/regex?raw="+url.QueryEscape(`path:home/sub unique-\w+-xyz`), &rx); code != 200 {
		t.Fatalf("regex with path: %d", code)
	}
	if rx.Mode != "regex" || len(rx.Hits) != 1 || rx.Hits[0].Path != "home/sub/second.md" {
		t.Fatalf("path: hits: %+v", rx)
	}
	if code := e.get(t, "/api/search/regex?raw="+url.QueryEscape(`space:work stats`), &rx); code != 200 {
		t.Fatalf("regex with space: %d", code)
	}
	if len(rx.Hits) != 1 || !strings.HasPrefix(rx.Hits[0].Path, "work/") {
		t.Fatalf("space: hits: %+v", rx)
	}
	// A path with nothing under it answers empty, like the FTS side.
	if code := e.get(t, "/api/search/regex?raw="+url.QueryEscape(`path:home/nope stats`), &rx); code != 200 {
		t.Fatalf("missing path: %d", code)
	}
	if len(rx.Hits) != 0 {
		t.Fatalf("missing path hits: %+v", rx)
	}
	// Only operators: no pattern left.
	var errResp map[string]string
	if code := e.get(t, "/api/search/regex?raw="+url.QueryEscape(`path:home/sub`), &errResp); code != 400 {
		t.Fatalf("patternless: %d", code)
	}
	// The old q&raw form still runs the whole string as the pattern.
	if code := e.get(t, "/api/search?raw="+url.QueryEscape(`unique-\w+-xyz`), &rx); code != 200 {
		t.Fatalf("legacy raw form: %d", code)
	}
	if len(rx.Hits) != 1 {
		t.Fatalf("legacy raw hits: %+v", rx)
	}
}
