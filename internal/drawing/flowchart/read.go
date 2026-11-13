package flowchart

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Item is one thing in a drawing, as an agent sees it.
type Item struct {
	ID                 string // agent-facing id
	Kind               string // "node", "edge", "subgraph", "free"
	Type               string // Excalidraw type; for nodes the shape token
	X, Y, W, H         int
	Elements           []string // Excalidraw element ids this item covers
	Text               string   // label, title, or free text
	Stroke, Fill, Line string
	From, To           string // edges; free arrows with one bound end ("" loose)
	Parent             string // subgraph id
	FileID             string // images
	arrow              bool
	width              float64
}

// View is a drawing read back.
type View struct {
	Graph Graph
	Items []Item // z-order
}

const (
	defStroke = "#1e1e1e"
	defFill   = "transparent"
)

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// shapeToken is the stored shape token while the element is still drawn as
// that token's primitive; once the owner changes its type or corners in
// Excalidraw, the token is derived from the element.
func shapeToken(e El) string {
	if s := e.YanaShape(); s != "" {
		typ, round := primitive(s)
		if typ == e.Type() && (typ != "rectangle" || (round != 0) == rounded(e)) {
			return s
		}
	}
	switch e.Type() {
	case "diamond":
		return "diamond"
	case "ellipse":
		return "circle"
	}
	if rounded(e) {
		return "round"
	}
	return "rect"
}

// lineOf is the Mermaid line of an arrow: thick wins over dotted.
func lineOf(e El) string {
	if e.num("strokeWidth") >= 4 {
		return "thick"
	}
	switch e.str("strokeStyle") {
	case "dashed", "dotted":
		return "dotted"
	}
	return "solid"
}

func isNodeEl(e El) bool {
	switch e.Type() {
	case "rectangle", "diamond", "ellipse":
		return e.YanaKind() != "subgraph"
	}
	return false
}

// Read classifies the live elements of a scene.
func Read(sceneJSON []byte) (View, error) {
	sc, err := ParseScene(sceneJSON)
	if err != nil {
		return View{}, err
	}
	live := sc.Live()
	liveByID := map[string]El{}
	for _, e := range live {
		liveByID[e.ID()] = e
	}
	// ids: Mermaid ids first, then derived ones.
	taken := map[string]bool{}
	ids := map[string]string{} // element id -> agent id
	for _, e := range live {
		if y := e.YanaID(); y != "" && (e.YanaKind() == "node" || e.YanaKind() == "subgraph") && !taken[y] && e.Type() != "text" {
			taken[y], ids[e.ID()] = true, y
		}
	}
	derive := func(e El) string {
		if id, ok := ids[e.ID()]; ok {
			return id
		}
		full := "x" + sanitize(e.ID())
		id := full
		if s := sanitize(e.ID()); len(s) > 8 {
			id = "x" + s[:8]
		}
		for n := 2; taken[id]; n++ {
			if id != full {
				id = full
			} else {
				id = full + "_" + strconv.Itoa(n)
			}
		}
		taken[id], ids[e.ID()] = true, id
		return id
	}

	// bound text of a container
	label := func(c El) (string, []string) {
		for _, id := range c.BoundIDs("text") {
			if t := liveByID[id]; t != nil && t.Type() == "text" && t.ContainerID() == c.ID() {
				return t.Text(), []string{id}
			}
		}
		return "", nil
	}

	var v View
	v.Graph.Dir = "TB"
	nodeEl := map[string]El{}
	for _, e := range live {
		if isNodeEl(e) {
			nodeEl[e.ID()] = e
			if d := e.yana("dir"); d != "" && v.Graph.Dir == "TB" {
				v.Graph.Dir = d
			}
		}
	}
	isSub := func(e El) bool { return e.Type() == "frame" || e.Type() == "rectangle" && e.YanaKind() == "subgraph" }
	var subs []El
	for _, e := range live {
		if isSub(e) {
			subs = append(subs, e)
		}
	}
	area := func(e El) float64 { return e.W() * e.H() }
	// parentOf: the smallest subgraph holding e (frame by frameId, box by centre).
	parentOf := func(e El, selfSub bool) string {
		cx, cy := e.X()+e.W()/2, e.Y()+e.H()/2
		best, bestA := "", math.MaxFloat64
		for _, s := range subs {
			if s.ID() == e.ID() || selfSub && area(s) <= area(e) {
				continue
			}
			in := s.Type() == "frame" && e.FrameID() == s.ID() ||
				s.Type() != "frame" && cx >= s.X() && cx <= s.X()+s.W() && cy >= s.Y() && cy <= s.Y()+s.H()
			if in && area(s) < bestA {
				best, bestA = ids[s.ID()], area(s)
			}
		}
		return best
	}
	for _, e := range live {
		if isSub(e) || isNodeEl(e) {
			derive(e)
		}
	}

	covered := map[string]bool{}
	edgeN := map[string]int{}
	pos := func(it *Item, e El) {
		it.X, it.Y, it.W, it.H = int(math.Round(e.X())), int(math.Round(e.Y())), int(math.Round(e.W())), int(math.Round(e.H()))
	}
	colours := func(it *Item, e El) {
		if s, _ := e["strokeColor"].(string); s != "" && s != defStroke {
			it.Stroke = s
		}
		if f, _ := e["backgroundColor"].(string); f != "" && f != defFill {
			it.Fill = f
		}
	}
	for _, e := range live {
		if e.Type() == "text" && e.ContainerID() != "" {
			if c := liveByID[e.ContainerID()]; c != nil && c.Type() != "text" {
				covered[e.ID()] = true // decided below; uncovered ones become free
			}
		}
	}
	used := map[string]bool{} // bound texts claimed by a container
	for _, e := range live {
		var it Item
		it.Elements = []string{e.ID()}
		pos(&it, e)
		switch {
		case isNodeEl(e):
			it.ID, it.Kind, it.Type = derive(e), "node", shapeToken(e)
			it.Text, _ = label(e)
			if _, ids := label(e); ids != nil {
				it.Elements = append(it.Elements, ids...)
				used[ids[0]] = true
			}
			colours(&it, e)
			it.Parent = parentOf(e, false)
			if w := e.num("strokeWidth"); w != 2 && w != 0 {
				it.width = w
			}
		case isSub(e):
			it.ID, it.Kind, it.Type = derive(e), "subgraph", e.Type()
			if e.Type() == "frame" {
				it.Text = e.str("name")
			} else {
				var l []string
				it.Text, l = label(e)
				it.Elements = append(it.Elements, l...)
				if len(l) > 0 {
					used[l[0]] = true
				}
			}
			it.Parent = parentOf(e, true)
		case e.Type() == "arrow" || e.Type() == "line":
			from, to := e.StartID(), e.EndID()
			if nodeEl[from] == nil {
				from = ""
			}
			if nodeEl[to] == nil {
				to = ""
			}
			it.Type = e.Type()
			it.Text, _ = label(e)
			if _, l := label(e); l != nil {
				it.Elements = append(it.Elements, l...)
				used[l[0]] = true
			}
			it.From, it.To = ids[from], ids[to]
			if e.Type() == "arrow" && from != "" && to != "" {
				it.Kind = "edge"
				k := it.From + "->" + it.To
				edgeN[k]++
				it.ID = k
				if edgeN[k] > 1 {
					it.ID = k + "#" + strconv.Itoa(edgeN[k])
				}
				it.Line = lineOf(e)
				it.arrow = e["endArrowhead"] != nil
				it.Type = "arrow"
			} else {
				it.Kind, it.ID = "free", derive(e)
				if e.Type() == "line" { // lines are never edges
					it.From, it.To = "", ""
				}
				colours(&it, e)
			}
		case e.Type() == "text":
			if used[e.ID()] || covered[e.ID()] {
				continue // folded into its container (checked again below)
			}
			it.Kind, it.ID, it.Type, it.Text = "free", derive(e), "text", e.Text()
			colours(&it, e)
		default:
			it.Kind, it.ID, it.Type = "free", derive(e), e.Type()
			it.FileID = e.str("fileId")
			colours(&it, e)
		}
		v.Items = append(v.Items, it)
	}
	// Texts whose container never claimed them (a bound text of a free shape) stay free.
	for _, e := range live {
		if e.Type() == "text" && covered[e.ID()] && !used[e.ID()] {
			it := Item{Kind: "free", ID: derive(e), Type: "text", Text: e.Text(), Elements: []string{e.ID()}}
			pos(&it, e)
			v.Items = append(v.Items, it)
		}
	}

	for _, it := range v.Items {
		switch it.Kind {
		case "node":
			l := it.Text
			v.Graph.Nodes = append(v.Graph.Nodes, Node{ID: it.ID, Label: &l, Shape: it.Type, Stroke: it.Stroke, Fill: it.Fill, StrokeWidth: it.width, Subgraph: it.Parent})
		case "edge":
			v.Graph.Edges = append(v.Graph.Edges, Edge{From: it.From, To: it.To, Label: it.Text, Line: it.Line, Arrow: it.arrow})
		case "subgraph":
			v.Graph.Subgraphs = append(v.Graph.Subgraphs, Subgraph{it.ID, it.Text, it.Parent})
		}
	}
	return v, nil
}

// escape makes s safe inside a quoted Mermaid label that Parse reads back unchanged.
func escape(s string) string {
	s = strings.NewReplacer(`"`, "#quot;", `\`, "#92;", "#", "#35;", "<", "#lt;", "\n", "<br>").Replace(s)
	if len(s) >= 2 && s[0] == '`' && s[len(s)-1] == '`' {
		s = "`" + s + "`" // Parse strips one pair of backticks
	}
	return `"` + s + `"`
}

func shapeBrackets(shape string) (open, close string) {
	for _, d := range shapeDefs {
		for i, s := range d.shapes {
			if s == shape {
				return d.open, d.closers[i]
			}
		}
	}
	return "[", "]"
}

// Mermaid is the flowchart part of Text(); Parse(v.Mermaid()) gives back v.Graph.
func (v View) Mermaid() string {
	g := v.Graph
	var b strings.Builder
	fmt.Fprintf(&b, "flowchart %s\n", g.Dir)
	for _, n := range g.Nodes {
		o, c := shapeBrackets(n.Shape)
		fmt.Fprintf(&b, "%s%s%s%s\n", n.ID, o, escape(*n.Label), c)
	}
	for _, e := range g.Edges {
		op := map[string][2]string{"solid": {"---", "-->"}, "dotted": {"-.-", "-.->"}, "thick": {"===", "==>"}}[e.Line]
		o := op[0]
		if e.Arrow {
			o = op[1]
		}
		if e.Label != "" {
			o += "|" + escape(e.Label) + "|"
		}
		fmt.Fprintf(&b, "%s %s %s\n", e.From, o, e.To)
	}
	var walk func(parent, indent string)
	walk = func(parent, indent string) {
		for _, s := range g.Subgraphs {
			if s.Parent != parent {
				continue
			}
			fmt.Fprintf(&b, "%ssubgraph %s [%s]\n", indent, s.ID, escape(s.Title))
			for _, n := range g.Nodes {
				if n.Subgraph == s.ID {
					fmt.Fprintf(&b, "%s  %s\n", indent, n.ID)
				}
			}
			walk(s.ID, indent+"  ")
			fmt.Fprintf(&b, "%send\n", indent)
		}
	}
	walk("", "")
	for _, n := range g.Nodes {
		var p []string
		if n.Stroke != "" {
			p = append(p, "stroke:"+n.Stroke)
		}
		if n.Fill != "" {
			p = append(p, "fill:"+n.Fill)
		}
		if n.StrokeWidth != 0 {
			p = append(p, "stroke-width:"+strconv.FormatFloat(n.StrokeWidth, 'f', -1, 64)+"px")
		}
		if len(p) > 0 {
			fmt.Fprintf(&b, "style %s %s\n", n.ID, strings.Join(p, ","))
		}
	}
	return b.String()
}

// Text is the Mermaid followed by a "layout:" section, one line per item.
func (v View) Text() string {
	var b strings.Builder
	b.WriteString(v.Mermaid())
	b.WriteString("\nlayout:\n")
	for _, it := range v.Items {
		geo := fmt.Sprintf("x=%d y=%d w=%d h=%d", it.X, it.Y, it.W, it.H)
		switch it.Kind {
		case "node":
			fmt.Fprintf(&b, "node %s %s %s", it.ID, it.Type, geo)
			if it.Parent != "" {
				b.WriteString(" in=" + it.Parent)
			}
			if it.Stroke != "" {
				b.WriteString(" stroke=" + it.Stroke)
			}
			if it.Fill != "" {
				b.WriteString(" fill=" + it.Fill)
			}
		case "edge":
			fmt.Fprintf(&b, "edge %s %s -> %s %s", it.ID, it.From, it.To, it.Line)
			if !it.arrow {
				b.WriteString(" noarrow")
			}
			if it.Text != "" {
				b.WriteString(" label=" + strconv.Quote(it.Text))
			}
		case "subgraph":
			kind := "box"
			if it.Type == "frame" {
				kind = "frame"
			}
			fmt.Fprintf(&b, "subgraph %s %s %s %s", it.ID, kind, strconv.Quote(it.Text), geo)
			if it.Parent != "" {
				b.WriteString(" in=" + it.Parent)
			}
		default:
			fmt.Fprintf(&b, "free %s %s %s", it.ID, it.Type, geo)
			if it.Text != "" {
				b.WriteString(" text=" + strconv.Quote(it.Text))
			}
			if it.From != "" {
				b.WriteString(" from=" + it.From)
			}
			if it.To != "" {
				b.WriteString(" to=" + it.To)
			}
			if it.Type == "image" {
				b.WriteString(" image")
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
