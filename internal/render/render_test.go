package render

import (
	"strings"
	"testing"
)

func TestMarkdownBasics(t *testing.T) {
	src := []byte("# Hello\n\nSome *text* with a [[Target Note|shown]] and [[plain]].\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```go\nfmt.Println(\"x\")\n```\n\n<script>alert(1)</script>\n\nFootnote[^1].\n\n[^1]: The note.\n")
	out, err := Markdown(src)
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	for _, want := range []string{
		`<h1 id="hello">Hello</h1>`,
		`<span class="wikilink" data-target="Target Note">shown</span>`,
		`<span class="wikilink" data-target="plain">plain</span>`,
		`<table>`,
		`class="chroma"`,
		`<!-- raw HTML omitted -->`,
		`class="footnotes"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in:\n%s", want, html)
		}
	}
	if strings.Contains(html, "<script>") {
		t.Error("raw HTML must be escaped")
	}
}

func TestWikilinkEdgeCases(t *testing.T) {
	cases := map[string]string{
		"[[a]]":           `data-target="a">a</span>`,
		"[[ a | b ]]":     `data-target="a">b</span>`,
		"[[]]":            "[[]]",
		"[[a":             "[[a",
		"[[a|]]":          `data-target="a">a</span>`,
		"[link](x) [[y]]": `data-target="y">y</span>`,
		"`[[not]]`":       "<code>[[not]]</code>",
	}
	for in, want := range cases {
		out, err := Markdown([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), want) {
			t.Errorf("%q: want %q in %q", in, want, out)
		}
	}
}

func TestTitleTagsPreview(t *testing.T) {
	body := []byte("# My Title #\n\nFirst para with #tag1 and #Tag1 and issue #12 and #multi/level.\n\n```\n#notatag\n```\n`#alsonot` foo#bar #tag2")
	if got := Title(body); got != "My Title" {
		t.Errorf("title: %q", got)
	}
	tags := Tags(body)
	want := []string{"tag1", "multi/level", "tag2"}
	if strings.Join(tags, ",") != strings.Join(want, ",") {
		t.Errorf("tags: %v want %v", tags, want)
	}
	p := Preview(body, 30)
	if strings.HasPrefix(p, "My Title") || !strings.HasPrefix(p, "First para") {
		t.Errorf("preview: %q", p)
	}
	if !strings.HasSuffix(p, "…") {
		t.Errorf("preview should be truncated: %q", p)
	}
	if Title([]byte("no heading")) != "" {
		t.Error("title without heading")
	}
}

func TestStripHTML(t *testing.T) {
	got := StripHTML([]byte("<html><style>p{}</style><body><h1>Hi</h1><script>x()</script><p>there</p></body></html>"))
	if got != "Hi there" {
		t.Errorf("%q", got)
	}
}

func TestWikiLinks(t *testing.T) {
	body := []byte("# Title\n\nSee [[target]] and [[docs/two.md|the other one]].\nAgain [[target]].\n\nCode ignores wikilinks:\n\n```md\n[[fenced]]\n```\n\nInline `[[spanned]]` too.\n")
	got := WikiLinks(body)
	want := []string{"target", "docs/two.md"}
	if len(got) != len(want) {
		t.Fatalf("WikiLinks = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("WikiLinks = %q, want %q", got, want)
		}
	}
}

func TestWikiLinksEmpty(t *testing.T) {
	if got := WikiLinks([]byte("plain text, no links")); len(got) != 0 {
		t.Fatalf("WikiLinks = %q, want none", got)
	}
}
