package crdt

import (
	"strings"
	"testing"
)

// The bind surface renders through the server's engine: every Phase 19
// construct comes out, raw HTML stays escaped, and the spans and
// data-line attributes the reader wires are present. This pins the
// phone's reader to the web's markup without a device.
func TestRenderMarkdownMatchesServerOutput(t *testing.T) {
	src := strings.Join([]string{
		"# Title",
		"",
		"> [!note] Callout\n> Body with [[Other note|alias]] and #tag/one.",
		"",
		"```mermaid",
		"graph TD; A-->B;",
		"```",
		"",
		"Inline $E = mc^2$ and display:",
		"",
		"$$\\int_0^1 x\\,dx$$",
		"",
		"| a | b |",
		"|---|---|",
		"| 1 | 2 |",
		"",
		"- [ ] open",
		"- [x] done",
		"",
		"<script>alert(1)</script> and [x](javascript:alert(1))",
		"",
		"```go",
		"func main() {}",
		"```",
	}, "\n")

	html, err := RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	for _, want := range []string{
		`<span class="wikilink" data-target="Other note">alias</span>`,
		`<span class="tag" data-tag="tag/one">#tag/one</span>`,
		`<pre class="mermaid">`,
		`<span class="math math-inline">`,
		`<div class="math math-display">`,
		`<table>`,
		`<input type="checkbox" disabled="" data-line="`,
		`class="chroma"`,
		`class="callout`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("render missing %q\nhtml: %s", want, html)
		}
	}
	if strings.Contains(html, "<script>") {
		t.Errorf("raw HTML was not escaped:\n%s", html)
	}
}

func TestRenderMarkdownError(t *testing.T) {
	if _, err := RenderMarkdown(""); err != nil {
		t.Errorf("empty body should render, got %v", err)
	}
}

func TestWikiLinks(t *testing.T) {
	src := "See [[One]] and [[Two|two shown]].\n\n```md\nnot a [[Link]]\n```\n"
	got := WikiLinks(src)
	want := "One\nTwo"
	if got != want {
		t.Fatalf("WikiLinks = %q, want %q", got, want)
	}
}
