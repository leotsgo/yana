package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata/rich")

// TestRichGolden renders each testdata/rich/*.md and compares it with the
// .html beside it. `go test ./internal/render -update` rewrites them.
func TestRichGolden(t *testing.T) {
	srcs, err := filepath.Glob("testdata/rich/*.md")
	if err != nil || len(srcs) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, src := range srcs {
		name := strings.TrimSuffix(filepath.Base(src), ".md")
		t.Run(name, func(t *testing.T) {
			in, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Markdown(in)
			if err != nil {
				t.Fatal(err)
			}
			golden := strings.TrimSuffix(src, ".md") + ".html"
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if string(got) != string(want) {
				t.Errorf("%s: output differs from golden\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
			}
		})
	}
}

func TestInlineMathRules(t *testing.T) {
	cases := map[string]string{
		"$x$":             `<span class="math math-inline">x</span>`,
		"$a b$":           `<span class="math math-inline">a b</span>`,
		"$ x$":            `$ x$`,
		"$x $":            `$x $`,
		"x $$ y":          `x $$ y`,
		"$5 and $10":      `$5 and $10`,
		"$5-$10":          `$5-$10`,
		"$x$1":            `$x$1`,
		"$\\$$":           `<span class="math math-inline">\$</span>`,
		"\\$x\\$":         `$x$`,
		"`$x$`":           `<code>$x$</code>`,
		"$a_1$ and $b_2$": `<span class="math math-inline">a_1</span> and <span class="math math-inline">b_2</span>`,
		"$a<b$":           `<span class="math math-inline">a&lt;b</span>`,
	}
	for in, want := range cases {
		out, err := Markdown([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), want) {
			t.Errorf("%q: got %s, want it to contain %s", in, out, want)
		}
	}
}

func TestRichDoesNotLeakIntoOtherPasses(t *testing.T) {
	body := []byte("> [!note] Title #tagged\n> Body with [[target]] and #tag.\n\n```mermaid\ngraph TD; A-->B\n```\n\n$x_1$ and $$\ny\n$$\n")
	if got := WikiLinks(body); len(got) != 1 || got[0] != "target" {
		t.Errorf("WikiLinks = %q", got)
	}
	if got := Tags(body); strings.Join(got, ",") != "tagged,tag" {
		t.Errorf("Tags = %q", got)
	}
	out, err := Markdown(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `class="chroma"`) {
		t.Errorf("mermaid fence reached the highlighter:\n%s", out)
	}
}
