// Package render turns markdown into HTML with goldmark. Raw HTML inside
// markdown is escaped, not passed through: a note is a text file anyone can
// edit, so its markup is not trusted until Phase 9 gives HTML its own origin.
package render

import (
	"bytes"
	"regexp"
	"strings"
	"sync"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var (
	once sync.Once
	md   goldmark.Markdown
)

func engine() goldmark.Markdown {
	once.Do(func() {
		md = goldmark.New(
			goldmark.WithExtensions(
				extension.GFM,
				extension.Footnote,
				extension.Typographer,
				highlighting.NewHighlighting(
					highlighting.WithStyle("friendly"),
					highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
				),
				&wikilinkExt{},
			),
			goldmark.WithParserOptions(parser.WithAutoHeadingID()),
			goldmark.WithRendererOptions(html.WithHardWraps()),
		)
	})
	return md
}

// Markdown renders a note body to HTML.
func Markdown(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := engine().Convert(body, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var h1 = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t#]*$`)

// Title returns the first H1 in body, or "".
func Title(body []byte) string {
	m := h1.FindSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(string(m[1]))
}

var (
	fence   = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~")
	inline  = regexp.MustCompile("`[^`\n]*`")
	tagRe   = regexp.MustCompile(`(?:^|[\s(])#([\p{L}\p{N}_][\p{L}\p{N}_/-]*)`)
	mdNoise = regexp.MustCompile(`[#*_>\[\]!]|\(([^)]*)\)`)
	spaces  = regexp.MustCompile(`\s+`)
)

// Tags extracts inline #tags from body text, ignoring code. Tags are
// lowercased and de-duplicated, in first-seen order. Pure numbers are not
// tags so "#1" in "issue #1" does not become one.
func Tags(body []byte) []string {
	clean := fence.ReplaceAll(body, nil)
	clean = inline.ReplaceAll(clean, nil)
	seen := map[string]struct{}{}
	var out []string
	for _, m := range tagRe.FindAllSubmatch(clean, -1) {
		tag := strings.ToLower(string(m[1]))
		if isNumeric(tag) {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// Preview returns a plain-text excerpt of about n runes, skipping the
// title line and most markdown punctuation.
func Preview(body []byte, n int) string {
	s := string(body)
	if m := h1.FindStringIndex(s); m != nil && m[0] == 0 {
		s = s[m[1]:]
	}
	s = fence.ReplaceAllString(s, " ")
	s = mdNoise.ReplaceAllString(s, "$1")
	s = spaces.ReplaceAllString(strings.TrimSpace(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

var htmlTag = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>|<[^>]+>`)

// StripHTML reduces an HTML note to text for indexing.
func StripHTML(src []byte) string {
	s := htmlTag.ReplaceAllString(string(src), " ")
	return spaces.ReplaceAllString(strings.TrimSpace(s), " ")
}

// --- wikilinks --------------------------------------------------------

// WikiLink is the AST node for [[target]] and [[target|display]]. Until
// Phase 5 wires resolution, it renders as a marked span rather than an
// anchor.
type WikiLink struct {
	ast.BaseInline
	Target  []byte
	Display []byte
}

var kindWikiLink = ast.NewNodeKind("WikiLink")

// Kind implements ast.Node.
func (n *WikiLink) Kind() ast.NodeKind { return kindWikiLink }

// Dump implements ast.Node.
func (n *WikiLink) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{
		"Target": string(n.Target), "Display": string(n.Display),
	}, nil)
}

type wikilinkExt struct{}

func (e *wikilinkExt) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithInlineParsers(util.Prioritized(&wikilinkParser{}, 150)))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(&wikilinkRenderer{}, 500)))
}

type wikilinkParser struct{}

func (p *wikilinkParser) Trigger() []byte { return []byte{'['} }

func (p *wikilinkParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, seg := block.PeekLine()
	if len(line) < 4 || line[0] != '[' || line[1] != '[' {
		return nil
	}
	end := bytes.Index(line, []byte("]]"))
	if end < 2 {
		return nil
	}
	inner := line[2:end]
	if bytes.ContainsAny(inner, "[]\n") || len(bytes.TrimSpace(inner)) == 0 {
		return nil
	}
	target, display := inner, inner
	if i := bytes.IndexByte(inner, '|'); i >= 0 {
		target, display = inner[:i], inner[i+1:]
	}
	target = bytes.TrimSpace(target)
	display = bytes.TrimSpace(display)
	if len(target) == 0 {
		return nil
	}
	if len(display) == 0 {
		display = target
	}
	node := &WikiLink{Target: target, Display: display}
	// Keep the source segment so downstream passes can find the link text.
	node.AppendChild(node, ast.NewTextSegment(text.NewSegment(seg.Start+2, seg.Start+end)))
	block.Advance(end + 2)
	return node
}

type wikilinkRenderer struct{}

func (r *wikilinkRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindWikiLink, r.render)
}

func (r *wikilinkRenderer) render(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}
	n := node.(*WikiLink)
	_, _ = w.WriteString(`<span class="wikilink" data-target="`)
	_, _ = w.Write(util.EscapeHTML(n.Target))
	_, _ = w.WriteString(`">`)
	_, _ = w.Write(util.EscapeHTML(n.Display))
	_, _ = w.WriteString(`</span>`)
	return ast.WalkSkipChildren, nil
}
