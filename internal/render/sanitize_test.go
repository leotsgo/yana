package render

import (
	"strings"
	"testing"
)

func TestSanitizeHTMLStripsTheExecutable(t *testing.T) {
	cases := []struct {
		name string
		in   string
		gone []string // substrings that must not appear
	}{
		{
			name: "script block",
			in:   `<p>ok</p><script>fetch('/api/notes')</script>`,
			gone: []string{"<script", "fetch('/api/notes')"},
		},
		{
			name: "event handler",
			in:   `<p onclick="steal()">ok</p>`,
			gone: []string{"onclick"},
		},
		{
			name: "iframe and object",
			in:   `<iframe src="https://evil.example/x.html"></iframe><object data="https://evil.example/o"></object>`,
			gone: []string{"<iframe", "<object"},
		},
		{
			name: "form and inputs",
			in:   `<form action="https://evil.example"><input name="pw" type="password"></form>`,
			gone: []string{"<form", "<input"},
		},
		{
			name: "remote image",
			in:   `<img src="https://evil.example/pixel.png" alt="x">`,
			gone: []string{"evil.example"},
		},
		{
			name: "remote stylesheet link",
			in:   `<link rel="stylesheet" href="https://evil.example/s.css">`,
			gone: []string{"<link"},
		},
		{
			name: "absolute anchor href",
			in:   `<a href="https://evil.example/phish">click</a>`,
			gone: []string{"evil.example"},
		},
		{
			name: "javascript url",
			in:   `<a href="javascript:alert(1)">x</a>`,
			gone: []string{"javascript:"},
		},
		{
			name: "data url image",
			in:   `<img src="data:image/svg+xml;base64,PHN2Zz4=">`,
			gone: []string{"data:image"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := string(SanitizeHTML([]byte(tc.in)))
			for _, g := range tc.gone {
				if strings.Contains(out, g) {
					t.Fatalf("sanitized output still contains %q: %s", g, out)
				}
			}
		})
	}
}

func TestSanitizeHTMLKeepsTheDocument(t *testing.T) {
	in := `<h1>Dashboard</h1>
<style>.grid { display: grid; }</style>
<table><tr><td style="color: red;">1</td></tr></table>
<img src="_assets/chart.png" alt="chart" width="400">
<canvas width="600" height="400"></canvas>
<p>Read <a data-wikilink="metrics" data-wikilink-id="01ARZ3NDEKTSV4RRFFQ69G5FAV">the metrics</a>.</p>
<section><details open><summary>More</summary>text</details></section>
<ul><li>one</li></ul>`
	out := string(SanitizeHTML([]byte(in)))
	for _, want := range []string{
		"<h1>Dashboard</h1>",
		".grid { display: grid; }",
		"style=\"color: red;\"",
		`src="_assets/chart.png"`,
		"<canvas width=\"600\" height=\"400\">",
		`data-wikilink="metrics"`,
		`data-wikilink-id="01ARZ3NDEKTSV4RRFFQ69G5FAV"`,
		"<details open=\"\"",
		"<section>", "<table>", "<ul>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sanitized output lost %q: %s", want, out)
		}
	}
}

func TestSanitizeHTMLIsIdempotent(t *testing.T) {
	in := []byte(`<p style="margin:1em">x</p><script>bad()</script><img src="_assets/a.png">`)
	once := SanitizeHTML(in)
	twice := SanitizeHTML(once)
	if string(once) != string(twice) {
		t.Fatalf("not idempotent:\n%s\n%s", once, twice)
	}
}

func TestHTMLWikiLinks(t *testing.T) {
	in := []byte(`<p><a data-wikilink="metrics">m</a> <A DATA-WIKILINK="Meeting &amp; notes">n</A> <a data-wikilink="metrics">dup</a> <a href="_assets/x">not a link</a></p>`)
	got := HTMLWikiLinks(in)
	want := []string{"metrics", "Meeting & notes"}
	if len(got) != len(want) {
		t.Fatalf("HTMLWikiLinks = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("HTMLWikiLinks = %#v, want %#v", got, want)
		}
	}
}
