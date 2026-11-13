package flowchart

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type props struct {
	stroke, fill string
	width        float64
}

type use struct {
	line     int
	id, name string
}

type styleStmt struct {
	line int
	id   string
	p    props
}

type parser struct {
	g         Graph
	idx       map[string]int
	stack     []string
	stackLine []int
	line      int
	s         string // current statement
	i         int
	classDefs map[string]props
	uses      []use
	styles    []styleStmt
}

type shapeDef struct {
	open    string
	closers []string
	shapes  []string // shape per closer
}

// Order matters: longer openers first.
var shapeDefs = []shapeDef{
	{"(((", []string{")))"}, []string{"double-circle"}},
	{"((", []string{"))"}, []string{"circle"}},
	{"([", []string{"])"}, []string{"stadium"}},
	{"(", []string{")"}, []string{"round"}},
	{"[(", []string{")]"}, []string{"cylinder"}},
	{"[[", []string{"]]"}, []string{"subroutine"}},
	{"[/", []string{"/]", `\]`}, []string{"parallelogram", "trapezoid"}},
	{`[\`, []string{`\]`, "/]"}, []string{"parallelogram-alt", "trapezoid-alt"}},
	{"[", []string{"]"}, []string{"rect"}},
	{"{{", []string{"}}"}, []string{"hexagon"}},
	{"{", []string{"}"}, []string{"diamond"}},
	{">", []string{"]"}, []string{"asymmetric"}},
}

var brDecoder = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n")

var namedEntities = map[string]string{"quot": `"`, "amp": "&", "lt": "<", "gt": ">"}

// entityAt reports the decoded text and length of a Mermaid entity (#quot;,
// #amp;, #lt;, #gt; or #NNN; with a decimal code point) at the start of s.
func entityAt(s string) (string, int) {
	j := strings.IndexByte(s, ';')
	if len(s) < 3 || s[0] != '#' || j < 2 {
		return "", 0
	}
	name := s[1:j]
	if r, ok := namedEntities[name]; ok {
		return r, j + 1
	}
	if n, err := strconv.Atoi(name); err == nil && name[0] != '+' && name[0] != '-' && n > 0 && n <= unicode.MaxRune {
		return string(rune(n)), j + 1
	}
	return "", 0
}

// textDecoder turns <br> into newlines and decodes entities.
type decoder struct{}

func (decoder) Replace(s string) string {
	s = brDecoder.Replace(s)
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '#' {
			if t, n := entityAt(s[i:]); n > 0 {
				b.WriteString(t)
				i += n - 1
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

var textDecoder decoder

// Parse reads a Mermaid flowchart.
func Parse(src string) (Graph, error) {
	p := &parser{g: Graph{Dir: "TB"}, idx: map[string]int{}, classDefs: map[string]props{}}
	header := false
	for n, raw := range strings.Split(src, "\n") {
		p.line = n + 1
		for _, st := range splitStmts(raw) {
			st = strings.TrimSpace(st)
			if st == "" {
				continue
			}
			var err error
			if !header {
				header = true
				err = p.header(st)
			} else {
				err = p.stmt(st)
			}
			if err != nil {
				return Graph{}, err
			}
		}
	}
	if !header {
		return Graph{}, ErrNotFlowchart
	}
	if len(p.stack) > 0 {
		return Graph{}, &ParseError{p.stackLine[len(p.stack)-1], fmt.Sprintf("subgraph %s is never closed with end", p.stack[len(p.stack)-1])}
	}
	return p.g, p.resolve()
}

func (p *parser) errf(f string, a ...any) error {
	return &ParseError{p.line, fmt.Sprintf(f, a...)}
}

// splitStmts cuts a line at ";" and drops a trailing "%%" comment, leaving quoted text alone.
func splitStmts(l string) []string {
	var out []string
	start, inQ := 0, false
	for i := 0; i < len(l); i++ {
		c := l[i]
		switch {
		case inQ && c == '\\':
			i++
		case c == '"':
			inQ = !inQ
		case inQ:
		case c == '#': // a named entity such as #quot; is not a terminator; hex colours never match
			// ponytail: numeric entities (#35;) are only recognised inside quotes, outside they could be a colour like #000;
			if _, n := entityAt(l[i:]); n > 0 && namedEntities[l[i+1:i+n-1]] != "" {
				i += n - 1
			}
		case c == '%' && strings.HasPrefix(l[i:], "%%"):
			return append(out, l[start:i])
		case c == ';':
			out = append(out, l[start:i])
			start = i + 1
		}
	}
	return append(out, l[start:])
}

func (p *parser) header(st string) error {
	f := strings.Fields(st)
	if f[0] != "flowchart" && f[0] != "graph" {
		return ErrNotFlowchart
	}
	if len(f) > 2 {
		return p.errf("unexpected %q after the direction", f[2])
	}
	if len(f) == 2 {
		switch f[1] {
		case "TD", "TB":
			p.g.Dir = "TB"
		case "BT", "LR", "RL":
			p.g.Dir = f[1]
		default:
			return p.errf("unknown direction %q (use TB, BT, LR or RL)", f[1])
		}
	}
	return nil
}

func (p *parser) stmt(st string) error {
	w := st
	if i := strings.IndexAny(st, " \t"); i >= 0 {
		w = st[:i]
	}
	rest := strings.TrimSpace(st[len(w):])
	switch w {
	case "end":
		if rest != "" {
			return p.errf("unexpected %q after end", rest)
		}
		if len(p.stack) == 0 {
			return p.errf("end without a subgraph")
		}
		p.stack, p.stackLine = p.stack[:len(p.stack)-1], p.stackLine[:len(p.stackLine)-1]
		return nil
	case "subgraph":
		return p.subgraph(rest)
	case "direction", "click", "linkStyle":
		return nil
	case "style":
		id, ps, _ := strings.Cut(rest, " ")
		pr, err := p.parseProps(ps)
		if err != nil || id == "" {
			return orErr(err, p.errf("style needs a node id"))
		}
		p.styles = append(p.styles, styleStmt{p.line, id, pr})
		return nil
	case "classDef":
		names, ps, _ := strings.Cut(rest, " ")
		pr, err := p.parseProps(ps)
		if err != nil || names == "" {
			return orErr(err, p.errf("classDef needs a name"))
		}
		for _, n := range strings.Split(names, ",") {
			p.classDefs[n] = pr
		}
		return nil
	case "class":
		k := strings.LastIndexAny(rest, " \t")
		if k < 0 {
			return p.errf("class needs node ids and a class name")
		}
		for _, id := range strings.Split(rest[:k], ",") {
			p.uses = append(p.uses, use{p.line, strings.TrimSpace(id), rest[k+1:]})
		}
		return nil
	}
	return p.edgeStmt(st)
}

func orErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

func (p *parser) parseProps(s string) (props, error) {
	var pr props
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "fill":
			pr.fill = expandColor(v)
		case "stroke":
			pr.stroke = expandColor(v)
		case "stroke-width":
			w, err := strconv.ParseFloat(strings.TrimSuffix(v, "px"), 64)
			if err != nil {
				return pr, p.errf("bad stroke-width %q", v)
			}
			pr.width = w
		}
	}
	return pr, nil
}

func expandColor(c string) string {
	if len(c) == 4 && c[0] == '#' {
		return "#" + string([]byte{c[1], c[1], c[2], c[2], c[3], c[3]})
	}
	return c
}

func (p *parser) resolve() error {
	apply := func(n *Node, pr props) {
		if pr.stroke != "" {
			n.Stroke = pr.stroke
		}
		if pr.fill != "" {
			n.Fill = pr.fill
		}
		if pr.width != 0 {
			n.StrokeWidth = pr.width
		}
	}
	for _, u := range p.uses {
		i, ok := p.idx[u.id]
		if !ok {
			return &ParseError{u.line, "unknown node " + u.id}
		}
		d, ok := p.classDefs[u.name]
		if !ok {
			return &ParseError{u.line, "unknown class " + u.name}
		}
		apply(&p.g.Nodes[i], d)
	}
	for _, s := range p.styles {
		i, ok := p.idx[s.id]
		if !ok {
			return &ParseError{s.line, "unknown node " + s.id}
		}
		apply(&p.g.Nodes[i], s.p)
	}
	return nil
}

func (p *parser) subgraph(rest string) error {
	id, title := "", ""
	if strings.HasPrefix(rest, `"`) {
		t, _, end, ok := readText(rest, 0, []string{""})
		if !ok || strings.TrimSpace(rest[end:]) != "" {
			return p.errf("unclosed \" in subgraph title")
		}
		title = t
		id = strings.Map(func(r rune) rune {
			if r < 128 && (r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
				return r
			}
			return '_'
		}, t)
	} else {
		id = rest[:idEnd(rest, 0)]
		title = id
		if r := strings.TrimSpace(rest[len(id):]); r != "" {
			if r[0] != '[' {
				return p.errf("unexpected %q in subgraph %s", r, id)
			}
			t, _, end, ok := readText(r, 1, []string{"]"})
			if !ok {
				return p.errf("unclosed [ in subgraph %s", id)
			}
			if strings.TrimSpace(r[end:]) != "" {
				return p.errf("unexpected %q after subgraph %s", strings.TrimSpace(r[end:]), id)
			}
			title = t
		}
	}
	if id == "" {
		return p.errf("subgraph needs a name")
	}
	parent := ""
	if len(p.stack) > 0 {
		parent = p.stack[len(p.stack)-1]
	}
	p.g.Subgraphs = append(p.g.Subgraphs, Subgraph{id, title, parent})
	p.stack, p.stackLine = append(p.stack, id), append(p.stackLine, p.line)
	return nil
}

func isIDRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// idEnd returns the end of the run of id characters starting at i.
func idEnd(s string, i int) int {
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isIDRune(r) {
			break
		}
		i += size
	}
	return i
}

func at(s string, j int, c string) bool {
	if c == "" {
		return j == len(s)
	}
	return strings.HasPrefix(s[j:], c)
}

// readText reads a label starting at i up to the first of closers (a "" closer
// means end of input). A leading quote makes the text opaque: it ends at the
// first quote that is followed by a closer. Returns the decoded text, the index
// of the matched closer, the index after it, and whether one was found.
func readText(s string, i int, closers []string) (text string, which, end int, ok bool) {
	j := i
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	match := func(k int) int {
		for n, c := range closers {
			if at(s, k, c) {
				return n
			}
		}
		return -1
	}
	if j < len(s) && s[j] == '"' {
		var b strings.Builder
		for k := j + 1; k < len(s); k++ {
			switch {
			case s[k] == '\\' && k+1 < len(s) && s[k+1] == '"':
				b.WriteByte('"')
				k++
			case s[k] == '"':
				after := k + 1
				for after < len(s) && (s[after] == ' ' || s[after] == '\t') {
					after++
				}
				if n := match(after); n >= 0 {
					t := textDecoder.Replace(b.String())
					if len(t) >= 2 && t[0] == '`' && t[len(t)-1] == '`' {
						t = t[1 : len(t)-1]
					}
					return t, n, after + len(closers[n]), true
				}
				b.WriteByte('"') // a quote inside the label
			default:
				b.WriteByte(s[k])
			}
		}
		return "", 0, 0, false
	}
	for k := i; k <= len(s); k++ {
		if n := match(k); n >= 0 {
			return textDecoder.Replace(strings.TrimSpace(s[i:k])), n, k + len(closers[n]), true
		}
	}
	return "", 0, 0, false
}

func (p *parser) skip() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *parser) edgeStmt(st string) error {
	p.s, p.i = st, 0
	srcs, err := p.group()
	if err != nil {
		return err
	}
	for {
		p.skip()
		if p.i >= len(p.s) {
			return nil
		}
		line, arrow, label, err := p.op()
		if err != nil {
			return err
		}
		dsts, err := p.group()
		if err != nil {
			return err
		}
		for _, f := range srcs {
			for _, t := range dsts {
				p.g.Edges = append(p.g.Edges, Edge{f, t, label, line, arrow})
			}
		}
		srcs = dsts
	}
}

func (p *parser) group() ([]string, error) {
	var ids []string
	for {
		id, err := p.node()
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
		p.skip()
		if p.i < len(p.s) && p.s[p.i] == '&' {
			p.i++
			continue
		}
		return ids, nil
	}
}

func (p *parser) node() (string, error) {
	p.skip()
	end := idEnd(p.s, p.i)
	if end == p.i {
		if p.i >= len(p.s) {
			return "", p.errf("expected a node at the end of %q", p.s)
		}
		return "", p.errf("unexpected %q", p.s[p.i:])
	}
	id := p.s[p.i:end]
	p.i = end
	var label *string
	shape := ""
	for _, d := range shapeDefs {
		if !strings.HasPrefix(p.s[p.i:], d.open) {
			continue
		}
		t, n, e, ok := readText(p.s, p.i+len(d.open), d.closers)
		if !ok {
			return "", p.errf("unclosed %s in node %s", d.open, id)
		}
		label, shape, p.i = &t, d.shapes[n], e
		break
	}
	if strings.HasPrefix(p.s[p.i:], ":::") {
		e := p.i + 3
		for e < len(p.s) && (p.s[e] == '-' || isIDRune(rune(p.s[e]))) {
			e++
		}
		p.uses = append(p.uses, use{p.line, id, p.s[p.i+3 : e]})
		p.i = e
	}
	i, ok := p.idx[id]
	if !ok {
		p.g.Nodes = append(p.g.Nodes, Node{ID: id})
		i = len(p.g.Nodes) - 1
		p.idx[id] = i
	}
	n := &p.g.Nodes[i]
	if shape != "" {
		n.Shape, n.Label = shape, label
	}
	if n.Subgraph == "" && len(p.stack) > 0 {
		n.Subgraph = p.stack[len(p.stack)-1]
	}
	return id, nil
}

func (p *parser) op() (line string, arrow bool, label string, err error) {
	s := p.s[p.i:]
	var n int
	var ok bool
	switch {
	case strings.HasPrefix(s, "=="):
		line = "thick"
		arrow, label, n, ok = dashOp(s, '=')
	case strings.HasPrefix(s, "-."):
		line = "dotted"
		arrow, label, n, ok = dotOp(s)
	case strings.HasPrefix(s, "--"):
		line = "solid"
		arrow, label, n, ok = dashOp(s, '-')
	}
	if !ok {
		return "", false, "", p.errf("unexpected %q", s)
	}
	p.i += n
	p.skip()
	if p.i < len(p.s) && p.s[p.i] == '|' {
		t, _, e, ok := readText(p.s, p.i+1, []string{"|"})
		if !ok {
			return "", false, "", p.errf("unclosed | in edge label")
		}
		label, p.i = t, e
	}
	return line, arrow, label, nil
}

func edgeText(t string) string {
	t = strings.TrimSpace(t)
	if len(t) >= 2 && t[0] == '"' && t[len(t)-1] == '"' {
		t = t[1 : len(t)-1]
	}
	return textDecoder.Replace(t)
}

// dashOp reads -->, ---, -- text --> and -- text --- (c is '-' or '=').
func dashOp(s string, c byte) (arrow bool, label string, n int, ok bool) {
	k := 0
	for k < len(s) && s[k] == c {
		k++
	}
	if k < len(s) && s[k] == '>' {
		return true, "", k + 1, true
	}
	if k >= 3 {
		return false, "", k, true
	}
	for j := k; j < len(s); j++ {
		if s[j] != c {
			continue
		}
		m := j
		for m < len(s) && s[m] == c {
			m++
		}
		if m < len(s) && s[m] == '>' && m-j >= 2 {
			return true, edgeText(s[k:j]), m + 1, true
		}
		if m-j >= 3 {
			return false, edgeText(s[k:j]), m, true
		}
		j = m - 1
	}
	return false, "", 0, false
}

// dotOp reads -.->, -.- and -. text .-> / -. text .-
func dotOp(s string) (arrow bool, label string, n int, ok bool) {
	if strings.HasPrefix(s, "-.-") {
		if strings.HasPrefix(s, "-.->") {
			return true, "", 4, true
		}
		return false, "", 3, true
	}
	j := strings.Index(s[2:], ".-")
	if j < 0 {
		return false, "", 0, false
	}
	label, n = edgeText(s[2:2+j]), 2+j+2
	if strings.HasPrefix(s[n:], ">") {
		return true, label, n + 1, true
	}
	return false, label, n, true
}
