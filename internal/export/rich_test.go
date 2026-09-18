package export

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"testing/fstest"
)

// stubRich stands in for the web build's dist/export.
func stubRich() fstest.MapFS {
	return fstest.MapFS{
		"mermaid.js":                           {Data: []byte("/* mermaid stub */")},
		"katex.js":                             {Data: []byte("/* katex stub */")},
		"katex-style.css":                      {Data: []byte(`@font-face{font-family:KaTeX_Main;src:url("./katex-fonts/KaTeX_Main-Regular.woff2") format("woff2")}`)},
		"katex-fonts/KaTeX_Main-Regular.woff2": {Data: []byte("WOFF2")},
	}
}

func richFixture() map[string]string {
	return map[string]string{
		"home/.space.md":    "---\nname: Home\n---\n",
		"home/diagram.md":   "---\nid: 01JQ8X4K2M9P7R3T5V6W8Y0Z1D\n---\n# Diagram\n\n```mermaid\nflowchart LR\n  A --> B\n```\n\n> [!warning] Careful\n> A callout.\n",
		"home/math.md":      "---\nid: 01JQ8X4K2M9P7R3T5V6W8Y0Z1M\n---\n# Math\n\nInline $x$ and\n\n$$\nE = mc^2\n$$\n",
		"home/sub/plain.md": "# Plain\n\nNothing to draw.\n",
	}
}

func TestSiteZipCarriesRuntimesPagesNeed(t *testing.T) {
	e := newExportEnv(t, richFixture())
	e.deps.Rich = stubRich()
	var buf bytes.Buffer
	if _, err := e.deps.SiteZip(context.Background(), "home", "", &buf); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	names := unzip(t, buf.Bytes(), dir)
	for _, want := range []string{"mermaid.js", "katex.js", "katex.css", "katex-fonts/KaTeX_Main-Regular.woff2"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Errorf("%s missing from the site: %v", want, names)
		}
	}
	checkHrefs(t, dir)

	diagram := readSite(t, dir, "diagram.html")
	if !strings.Contains(diagram, `<pre class="mermaid">flowchart LR`) || !strings.Contains(diagram, `<script src="mermaid.js"></script>`) {
		t.Errorf("diagram page lacks the fence or the script:\n%s", diagram)
	}
	if strings.Contains(diagram, "katex") {
		t.Errorf("diagram page loads KaTeX without math:\n%s", diagram)
	}
	if !strings.Contains(diagram, `class="callout callout-warning"`) {
		t.Errorf("callout missing from the page:\n%s", diagram)
	}

	math := readSite(t, dir, "math.html")
	if !strings.Contains(math, `<link rel="stylesheet" href="katex.css">`) || !strings.Contains(math, `<script src="katex.js"></script>`) {
		t.Errorf("math page lacks the stylesheet or the script:\n%s", math)
	}
	if strings.Contains(math, "mermaid.js") {
		t.Errorf("math page loads mermaid without a diagram:\n%s", math)
	}

	plain := readSite(t, dir, "sub/plain.html")
	if strings.Contains(plain, "mermaid.js") || strings.Contains(plain, "katex") {
		t.Errorf("plain page loads a runtime it does not need:\n%s", plain)
	}
}

func TestSiteZipSkipsRuntimesNobodyNeeds(t *testing.T) {
	e := newExportEnv(t, map[string]string{"home/plain.md": "# Plain\n\nText.\n"})
	e.deps.Rich = stubRich()
	var buf bytes.Buffer
	if _, err := e.deps.SiteZip(context.Background(), "home", "", &buf); err != nil {
		t.Fatal(err)
	}
	for _, n := range unzip(t, buf.Bytes(), t.TempDir()) {
		if n == "mermaid.js" || n == "katex.js" || n == "katex.css" || strings.HasPrefix(n, "katex-fonts/") {
			t.Errorf("%s shipped with no page that needs it", n)
		}
	}
}

func TestSingleNoteInlinesRuntimes(t *testing.T) {
	e := newExportEnv(t, richFixture())
	e.deps.Rich = stubRich()
	out, err := e.deps.SingleNote(context.Background(), "01JQ8X4K2M9P7R3T5V6W8Y0Z1M")
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	for _, want := range []string{
		"<script>/* katex stub */</script>",
		`url("data:font/woff2;base64,V09GRjI=")`,
		`<div class="math math-display">E = mc^2</div>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("single export missing %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "mermaid stub") || strings.Contains(html, "katex-fonts/") {
		t.Errorf("single export carries mermaid or a file reference:\n%s", html)
	}

	out, err = e.deps.SingleNote(context.Background(), "01JQ8X4K2M9P7R3T5V6W8Y0Z1D")
	if err != nil {
		t.Fatal(err)
	}
	html = string(out)
	if !strings.Contains(html, "<script>/* mermaid stub */</script>") || strings.Contains(html, "katex stub") {
		t.Errorf("diagram export should carry mermaid only:\n%s", html)
	}
}

func TestExportsWithoutRuntimesKeepTheSource(t *testing.T) {
	e := newExportEnv(t, richFixture())
	e.deps.Rich = nil
	out, err := e.deps.SingleNote(context.Background(), "01JQ8X4K2M9P7R3T5V6W8Y0Z1D")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `<pre class="mermaid">`) || strings.Contains(string(out), "<script>") {
		t.Errorf("export without a runtime should show the source and no script:\n%s", out)
	}
	var buf bytes.Buffer
	if _, err := e.deps.SiteZip(context.Background(), "home", "", &buf); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	unzip(t, buf.Bytes(), dir)
	checkHrefs(t, dir)
}
