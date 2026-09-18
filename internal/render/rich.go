// Mermaid fences, callouts and math: the three constructs that make the
// documentation people and agents actually write read better than plain
// text. Each is a small goldmark extension. The server only marks them
// in the HTML; the client (and the exports, with the same bundled
// scripts) draws the diagram and typesets the math, so the render stays
// deterministic and cheap here and works with no network anywhere.
package render

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// --- mermaid ------------------------------------------------------------

// MermaidBlock is a ```mermaid fence. It replaces the fenced code block in
// the tree before rendering, so the highlighter never sees it, and renders
// as <pre class="mermaid"> holding the escaped source. A page with no
// script shows the source; the client swaps in the diagram.
type MermaidBlock struct {
	ast.BaseBlock
}

var kindMermaidBlock = ast.NewNodeKind("MermaidBlock")

// Kind implements ast.Node.
func (n *MermaidBlock) Kind() ast.NodeKind { return kindMermaidBlock }

// Dump implements ast.Node.
func (n *MermaidBlock) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

// IsRaw implements ast.Node: the source is not parsed for inlines.
func (n *MermaidBlock) IsRaw() bool { return true }

type mermaidExt struct{}

func (e *mermaidExt) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithASTTransformers(util.Prioritized(&mermaidTransformer{}, 100)))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(&mermaidRenderer{}, 100)))
}

type mermaidTransformer struct{}

func (t *mermaidTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	var fences []*ast.FencedCodeBlock
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if fc, ok := n.(*ast.FencedCodeBlock); ok && entering {
			if bytes.Equal(fc.Language(reader.Source()), []byte("mermaid")) {
				fences = append(fences, fc)
			}
		}
		return ast.WalkContinue, nil
	})
	for _, fc := range fences {
		m := &MermaidBlock{}
		m.SetLines(fc.Lines())
		fc.Parent().ReplaceChild(fc.Parent(), fc, m)
	}
}

type mermaidRenderer struct{}

func (r *mermaidRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMermaidBlock, r.render)
}

func (r *mermaidRenderer) render(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString(`<pre class="mermaid">`)
	writeLines(w, source, node)
	_, _ = w.WriteString("</pre>\n")
	return ast.WalkContinue, nil
}

// writeLines writes a block's source lines, escaped.
func writeLines(w util.BufWriter, source []byte, n ast.Node) {
	l := n.Lines().Len()
	for i := 0; i < l; i++ {
		line := n.Lines().At(i)
		_, _ = w.Write(util.EscapeHTML(line.Value(source)))
	}
}

// --- callouts -----------------------------------------------------------

// Callout is a blockquote whose first line names a kind: `> [!warning]`,
// optionally followed by a title and, before the title, a `-` (folded)
// or `+` (open) that makes it foldable. Its children are the rest of
// the quote, parsed as usual.
type Callout struct {
	ast.BaseBlock
	Name     string // the kind, lower-cased: one of calloutKinds or anything the author wrote
	Title    string // the rest of the first line, or "" for the kind's own name
	Foldable bool
	Folded   bool
}

var kindCallout = ast.NewNodeKind("Callout")

// Kind implements ast.Node.
func (n *Callout) Kind() ast.NodeKind { return kindCallout }

// Dump implements ast.Node.
func (n *Callout) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Name": n.Name, "Title": n.Title}, nil)
}

// calloutKinds are the kinds with an icon and a colour of their own.
// Anything else renders as a plain callout titled with the kind.
var calloutKinds = map[string]bool{
	"note": true, "tip": true, "warning": true, "danger": true, "info": true, "question": true,
}

// calloutIcons are inline SVG bodies on Lucide's 24-unit grid, matching
// the app's icon set. They ride in the HTML so exports carry them too.
var calloutIcons = map[string]string{
	"note":     `<path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z"/><path d="m15 5 4 4"/>`,
	"tip":      `<path d="M15 14c.2-1 .7-1.7 1.5-2.5 1-.9 1.5-2.2 1.5-3.5A6 6 0 0 0 6 8c0 1 .2 2.2 1.5 3.5.7.7 1.3 1.5 1.5 2.5"/><path d="M9 18h6"/><path d="M10 22h4"/>`,
	"warning":  `<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/>`,
	"danger":   `<circle cx="12" cy="12" r="10"/><path d="m15 9-6 6"/><path d="m9 9 6 6"/>`,
	"info":     `<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>`,
	"question": `<circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><path d="M12 17h.01"/>`,
	"":         `<path d="M4 6h16"/><path d="M4 12h10"/><path d="M4 18h13"/>`,
}

// calloutHead matches what follows the `>` on a callout's first line.
var calloutHead = regexp.MustCompile(`^\[!([A-Za-z][A-Za-z0-9_-]*)\]([-+]?)(?:[ \t]+(.*?))?[ \t]*$`)

type calloutExt struct{}

func (e *calloutExt) Extend(m goldmark.Markdown) {
	// Before the blockquote parser (800): a quote that opens with a
	// kind is a callout, every other quote is untouched.
	m.Parser().AddOptions(parser.WithBlockParsers(util.Prioritized(&calloutParser{}, 700)))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(&calloutRenderer{}, 500)))
}

type calloutParser struct{}

func (p *calloutParser) Trigger() []byte { return []byte{'>'} }

// marker consumes a blockquote marker (`>` and one optional space) the
// way goldmark's own blockquote parser does, and reports whether there
// was one.
func (p *calloutParser) marker(reader text.Reader) bool {
	line, _ := reader.PeekLine()
	w, pos := util.IndentWidth(line, reader.LineOffset())
	if w > 3 || pos >= len(line) || line[pos] != '>' {
		return false
	}
	pos++
	if pos >= len(line) || line[pos] == '\n' {
		reader.Advance(pos)
		return true
	}
	reader.Advance(pos)
	if line[pos] == ' ' || line[pos] == '\t' {
		padding := 0
		if line[pos] == '\t' {
			padding = util.TabWidth(reader.LineOffset()) - 1
		}
		reader.AdvanceAndSetPadding(1, padding)
	}
	return true
}

func (p *calloutParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	// Look before consuming: a plain quote must reach the blockquote
	// parser with the reader where it was.
	w, pos := util.IndentWidth(line, reader.LineOffset())
	if w > 3 || pos >= len(line) || line[pos] != '>' {
		return nil, parser.NoChildren
	}
	rest := bytes.TrimLeft(line[pos+1:], " \t")
	m := calloutHead.FindSubmatch(bytes.TrimRight(rest, "\r\n"))
	if m == nil {
		return nil, parser.NoChildren
	}
	p.marker(reader)
	// The rest of the line is the head; the children start on the next.
	l, _ := reader.PeekLine()
	reader.Advance(len(l) - 1)
	n := &Callout{
		Name:     strings.ToLower(string(m[1])),
		Title:    strings.TrimSpace(string(m[3])),
		Foldable: len(m[2]) > 0,
		Folded:   string(m[2]) == "-",
	}
	return n, parser.HasChildren
}

func (p *calloutParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	if p.marker(reader) {
		return parser.Continue | parser.HasChildren
	}
	return parser.Close
}

func (p *calloutParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}

func (p *calloutParser) CanInterruptParagraph() bool { return true }

func (p *calloutParser) CanAcceptIndentedLine() bool { return false }

type calloutRenderer struct{}

func (r *calloutRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindCallout, r.render)
}

func (r *calloutRenderer) render(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*Callout)
	tag, head := "div", "p"
	if n.Foldable {
		tag, head = "details", "summary"
	}
	if !entering {
		_, _ = w.WriteString("</div></" + tag + ">\n")
		return ast.WalkContinue, nil
	}
	kind := n.Name
	if !calloutKinds[kind] {
		kind = ""
	}
	title := n.Title
	if title == "" {
		title = strings.ToUpper(n.Name[:1]) + n.Name[1:]
	}
	_, _ = w.WriteString(`<` + tag + ` class="callout`)
	if kind != "" {
		_, _ = w.WriteString(" callout-" + kind)
	}
	_, _ = w.WriteString(`" data-callout="`)
	_, _ = w.Write(util.EscapeHTML([]byte(n.Name)))
	_, _ = w.WriteString(`"`)
	if n.Foldable && !n.Folded {
		_, _ = w.WriteString(` open=""`)
	}
	_, _ = w.WriteString(`><` + head + ` class="callout-title">`)
	_, _ = w.WriteString(`<svg class="callout-icon" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">`)
	_, _ = w.WriteString(calloutIcons[kind])
	_, _ = w.WriteString(`</svg>`)
	_, _ = w.Write(util.EscapeHTML([]byte(title)))
	_, _ = w.WriteString(`</` + head + `><div class="callout-body">` + "\n")
	return ast.WalkContinue, nil
}

// --- math ---------------------------------------------------------------

// MathBlock is a `$$` display block: the lines between an opening `$$`
// and a closing one, or a single `$$…$$` line. It renders as a div holding
// the escaped TeX; the client typesets it.
type MathBlock struct {
	ast.BaseBlock
}

var kindMathBlock = ast.NewNodeKind("MathBlock")

// Kind implements ast.Node.
func (n *MathBlock) Kind() ast.NodeKind { return kindMathBlock }

// Dump implements ast.Node.
func (n *MathBlock) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

// IsRaw implements ast.Node: TeX is not parsed for inlines.
func (n *MathBlock) IsRaw() bool { return true }

// MathInline is `$…$` on one line. The dollar signs in prices and shell
// variables stay dollar signs: the opener must be followed and the closer
// preceded by something other than whitespace, the closer must not run
// straight into a digit, and no backtick may sit between them.
type MathInline struct {
	ast.BaseInline
	Source []byte
}

var kindMathInline = ast.NewNodeKind("MathInline")

// Kind implements ast.Node.
func (n *MathInline) Kind() ast.NodeKind { return kindMathInline }

// Dump implements ast.Node.
func (n *MathInline) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Source": string(n.Source)}, nil)
}

type mathExt struct{}

func (e *mathExt) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithBlockParsers(util.Prioritized(&mathBlockParser{}, 700)),
		parser.WithInlineParsers(util.Prioritized(&mathInlineParser{}, 200)),
	)
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(&mathRenderer{}, 500)))
}

type mathBlockParser struct{}

var mathBlockKey = parser.NewContextKey()

func (p *mathBlockParser) Trigger() []byte { return []byte{'$'} }

func (p *mathBlockParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, seg := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 || !bytes.HasPrefix(line[pos:], []byte("$$")) {
		return nil, parser.NoChildren
	}
	n := &MathBlock{}
	rest := bytes.TrimSpace(line[pos+2:])
	if len(rest) > 0 {
		// One line: `$$ E = mc^2 $$`. Anything else after the opener is
		// not a display block.
		if !bytes.HasSuffix(rest, []byte("$$")) || len(rest) < 3 {
			return nil, parser.NoChildren
		}
		if s := trimmedSegment(line, seg, pos+2, len(line)); s != nil {
			n.Lines().Append(*s)
		}
		// Closed on its own line: the next Continue ends it.
		pc.Set(mathBlockKey, n)
		return n, parser.NoChildren
	}
	return n, parser.NoChildren
}

// trimmedSegment is the source segment of line[from:to] with the
// surrounding whitespace (and any trailing `$$`) trimmed, or nil when
// nothing is left.
func trimmedSegment(line []byte, seg text.Segment, from, to int) *text.Segment {
	for to > from && (isSpace(line[to-1]) || line[to-1] == '\n' || line[to-1] == '\r') {
		to--
	}
	if to >= from+2 && line[to-1] == '$' && line[to-2] == '$' {
		to -= 2
	}
	for to > from && isSpace(line[to-1]) {
		to--
	}
	for from < to && isSpace(line[from]) {
		from++
	}
	if from >= to {
		return nil
	}
	s := text.NewSegment(seg.Start+from, seg.Start+to)
	return &s
}

func (p *mathBlockParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	if pc.Get(mathBlockKey) == node {
		pc.Set(mathBlockKey, nil)
		return parser.Close
	}
	line, seg := reader.PeekLine()
	trimmed := bytes.TrimRight(line, " \t\r\n")
	if bytes.HasSuffix(trimmed, []byte("$$")) {
		// The closing line may carry the last of the math before the
		// delimiter.
		if s := trimmedSegment(line, seg, 0, len(trimmed)); s != nil {
			node.Lines().Append(*s)
		}
		// The closer is ours; leave only the line end for the parser.
		reader.Advance(len(line) - 1)
		return parser.Close
	}
	node.Lines().Append(seg)
	return parser.Continue | parser.NoChildren
}

func (p *mathBlockParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}

func (p *mathBlockParser) CanInterruptParagraph() bool { return true }

func (p *mathBlockParser) CanAcceptIndentedLine() bool { return false }

type mathInlineParser struct{}

func (p *mathInlineParser) Trigger() []byte { return []byte{'$'} }

func (p *mathInlineParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if len(line) < 3 || line[0] != '$' || line[1] == '$' || isSpace(line[1]) {
		return nil
	}
	for i := 2; i < len(line); i++ {
		if line[i] == '\n' {
			return nil
		}
		if line[i] == '`' {
			return nil // a code span boundary: the span wins
		}
		if line[i] != '$' || line[i-1] == '\\' {
			continue
		}
		if isSpace(line[i-1]) {
			return nil
		}
		if i+1 < len(line) && line[i+1] >= '0' && line[i+1] <= '9' {
			return nil
		}
		n := &MathInline{Source: line[1:i]}
		block.Advance(i + 1)
		return n
	}
	return nil
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' }

type mathRenderer struct{}

func (r *mathRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMathBlock, r.renderBlock)
	reg.Register(kindMathInline, r.renderInline)
}

func (r *mathRenderer) renderBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString(`<div class="math math-display">`)
	l := node.Lines().Len()
	for i := 0; i < l; i++ {
		line := node.Lines().At(i)
		v := bytes.TrimRight(line.Value(source), "\r\n")
		if i > 0 {
			_ = w.WriteByte('\n')
		}
		_, _ = w.Write(util.EscapeHTML(v))
	}
	_, _ = w.WriteString("</div>\n")
	return ast.WalkContinue, nil
}

func (r *mathRenderer) renderInline(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}
	n := node.(*MathInline)
	_, _ = w.WriteString(`<span class="math math-inline">`)
	_, _ = w.Write(util.EscapeHTML(n.Source))
	_, _ = w.WriteString(`</span>`)
	return ast.WalkSkipChildren, nil
}
