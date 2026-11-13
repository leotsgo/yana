package flowchart

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Stats counts what Apply changed. Updated counts only elements whose
// content changed; a node that only gained an arrow binding is not counted.
type Stats struct {
	NodesAdded, NodesUpdated, NodesRemoved int
	EdgesAdded, EdgesUpdated, EdgesRemoved int
	OtherRemoved                           int // subgraphs and free items
}

// UnknownIDsError names removal ids the drawing does not have.
type UnknownIDsError struct{ IDs []string }

func (e *UnknownIDsError) Error() string { return "unknown ids: " + strings.Join(e.IDs, ", ") }

// Apply merges an agent's flowchart into an existing scene and removes the
// items named in remove (ids as Read reports them). Existing elements keep
// their place and size, except that a node box grows to fit a longer label
// and a subgraph grows to hold new members. New nodes are laid out next to
// the nodes they connect to. Only elements whose content changed are bumped.
// sceneJSON is not modified; on error nothing is returned.
func Apply(sceneJSON []byte, g Graph, remove []string, now time.Time) ([]byte, Stats, error) {
	v, err := Read(sceneJSON)
	if err != nil {
		return nil, Stats{}, err
	}
	s, err := ParseScene(sceneJSON)
	if err != nil {
		return nil, Stats{}, err
	}
	items := map[string]Item{}
	for _, it := range v.Items {
		items[it.ID] = it
	}
	var unknown []string
	for _, id := range remove {
		if _, ok := items[id]; !ok {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		return nil, Stats{}, &UnknownIDsError{unknown}
	}
	for _, n := range g.Nodes {
		if it, ok := items[n.ID]; ok && it.Kind != "node" {
			return nil, Stats{}, fmt.Errorf("%s is a %s in the drawing, not a node", n.ID, it.Kind)
		}
	}
	for _, sg := range g.Subgraphs {
		if it, ok := items[sg.ID]; ok && it.Kind != "subgraph" {
			return nil, Stats{}, fmt.Errorf("%s is a %s in the drawing, not a subgraph", sg.ID, it.Kind)
		}
	}

	a := &applier{s: s, g: g, items: items, now: now, byID: map[string]El{}, orig: map[string]bool{},
		dirty: map[string]bool{}, killed: map[string]bool{}, adds: map[string][]El{}, newNode: map[string]El{},
		removing: map[string]bool{}}
	for _, id := range remove {
		a.removing[id] = true // a removal wins over the Mermaid naming the same item
	}
	for _, e := range s.Elements {
		a.byID[e.ID()], a.orig[e.ID()] = e, true
	}
	for _, n := range g.Nodes {
		if it, ok := items[n.ID]; ok && !a.removing[n.ID] {
			a.updateNode(n, it)
		}
	}
	for _, sg := range g.Subgraphs {
		// ponytail: a title equal to the id reads as "no title given", so
		// `subgraph F` never renames F; renaming a subgraph to its own id isn't possible.
		if it, ok := items[sg.ID]; ok && sg.Title != sg.ID && !a.removing[sg.ID] {
			e := a.byID[it.Elements[0]]
			if e.Type() != "frame" {
				a.setLabel(e, sg.Title, "top", false)
			} else if e.str("name") != sg.Title {
				a.set(e, "name", sg.Title)
			}
		}
	}
	handled := a.addNodes()
	a.edges(handled)
	for _, id := range remove {
		a.remove(items[id])
	}
	if err := a.flush(); err != nil {
		return nil, Stats{}, err
	}
	a.unlink()
	for _, e := range s.Elements {
		if a.dirty[e.ID()] && a.orig[e.ID()] {
			e.Bump(now)
		}
	}
	out, err := s.JSON()
	if err != nil {
		return nil, Stats{}, err
	}
	return out, a.st, nil
}

type applier struct {
	s        *Scene
	g        Graph
	items    map[string]Item
	now      time.Time
	byID     map[string]El
	orig     map[string]bool // ids present before Apply
	dirty    map[string]bool // changed, bumped at the end if orig
	killed   map[string]bool // deleted by this Apply
	adds     map[string][]El // new elements by the frame they go before ("" = end)
	order    []string        // keys of adds in first-use order
	newNode  map[string]El   // new node elements by Mermaid id
	removing map[string]bool // ids in the remove list
	st       Stats
}

// set changes e[k] to v when it differs.
func (a *applier) set(e El, k string, v any) bool {
	if reflect.DeepEqual(e[k], v) {
		return false
	}
	e[k] = v
	a.dirty[e.ID()] = true
	return true
}

func (a *applier) add(frame string, els ...El) {
	if _, ok := a.adds[frame]; !ok {
		a.order = append(a.order, frame)
	}
	for _, e := range els {
		a.byID[e.ID()] = e
		if frame != "" {
			e["frameId"] = frame
		}
	}
	a.adds[frame] = append(a.adds[frame], els...)
}

// boundText is the live label of container c, or nil.
func (a *applier) boundText(c El) El {
	for _, id := range c.BoundIDs("text") {
		if t := a.byID[id]; t != nil && !t.Deleted() && t.Type() == "text" && t.ContainerID() == c.ID() {
			return t
		}
	}
	return nil
}

// setLabel makes text the label of container c ("" removes it). grow lets a
// node box grow, never shrink, to fit the text.
func (a *applier) setLabel(c El, text, valign string, grow bool) bool {
	t := a.boundText(c)
	if t != nil && t.Text() == text || t == nil && text == "" {
		return false
	}
	if text == "" {
		a.del(t)
		return true
	}
	w, h := TextSize(text, 20)
	if grow {
		a.set(c, "width", max(c.W(), w+40))
		a.set(c, "height", max(c.H(), h+30))
	}
	if t == nil {
		t = newText(c, text, valign, a.now)
		a.dirty[c.ID()] = true
		a.add(c.FrameID(), t)
		return true
	}
	x, y := labelPos(c, w, h, valign)
	for k, v := range map[string]any{"text": text, "originalText": text, "x": x, "y": y, "width": w, "height": h} {
		a.set(t, k, v)
	}
	return true
}

func sameColor(a, b string) bool { return strings.EqualFold(expandColor(a), expandColor(b)) }

func (a *applier) updateNode(n Node, it Item) {
	e := a.byID[it.Elements[0]]
	changed := false
	if n.Shape != "" && n.Shape != shapeToken(e) {
		typ, round := primitive(n.Shape)
		var rv any
		if round != 0 {
			rv = map[string]any{"type": round}
		}
		if typ != e.Type() || typ == "rectangle" && (round != 0) != rounded(e) {
			a.set(e, "type", typ)
			a.set(e, "roundness", rv)
		}
		cd, _ := e["customData"].(map[string]any)
		if cd == nil {
			cd = map[string]any{}
		}
		y, _ := cd["yana"].(map[string]any)
		if y == nil {
			y = map[string]any{}
		}
		y["shape"], cd["yana"], e["customData"] = n.Shape, y, cd
		a.dirty[e.ID()], changed = true, true
	}
	if n.Label != nil && a.setLabel(e, *n.Label, "middle", true) {
		changed = true
	}
	if n.Stroke != "" && !sameColor(e.str("strokeColor"), n.Stroke) {
		changed = a.set(e, "strokeColor", n.Stroke) || changed
	}
	if n.Fill != "" && !sameColor(e.str("backgroundColor"), n.Fill) {
		changed = a.set(e, "backgroundColor", n.Fill) || changed
	}
	if n.StrokeWidth != 0 && n.StrokeWidth != e.num("strokeWidth") {
		changed = a.set(e, "strokeWidth", n.StrokeWidth) || changed
	}
	if changed {
		a.st.NodesUpdated++
	}
}

func (a *applier) updateEdge(ed Edge, it Item) {
	e := a.byID[it.Elements[0]]
	changed := false
	if lineOf(e) != ed.Line {
		style, w := "solid", e.num("strokeWidth")
		if ed.Line == "dotted" {
			style = "dashed"
		}
		if ed.Line == "thick" {
			w = 4
		} else if w >= 4 {
			w = 2
		}
		a.set(e, "strokeStyle", style)
		a.set(e, "strokeWidth", w)
		changed = true
	}
	if (e["endArrowhead"] != nil) != ed.Arrow {
		var head any
		if ed.Arrow {
			head = "arrow"
		}
		changed = a.set(e, "endArrowhead", head) || changed
	}
	if a.setLabel(e, ed.Label, "middle", false) {
		changed = true
	}
	if changed {
		a.st.EdgesUpdated++
	}
}

// nodeEl is the element of node id, existing or new.
func (a *applier) nodeEl(id string) El {
	if it, ok := a.items[id]; ok {
		return a.byID[it.Elements[0]]
	}
	return a.newNode[id]
}

// edges updates the edges the drawing has and adds the others; handled are
// the indexes of edges already drawn with their new nodes.
func (a *applier) edges(handled map[int]bool) {
	seen := map[string]int{}
	for i, ed := range a.g.Edges {
		k := ed.From + "->" + ed.To
		seen[k]++
		id := k
		if seen[k] > 1 {
			id += "#" + strconv.Itoa(seen[k])
		}
		if a.removing[id] || a.removing[ed.From] || a.removing[ed.To] {
			continue
		}
		if it, ok := a.items[id]; ok && it.Kind == "edge" {
			a.updateEdge(ed, it)
			continue
		}
		if handled[i] {
			continue
		}
		from, to := a.nodeEl(ed.From), a.nodeEl(ed.To)
		if from == nil || to == nil {
			continue
		}
		a.dirty[from.ID()], a.dirty[to.ID()] = true, true
		frame := ""
		if from.FrameID() == to.FrameID() {
			frame = from.FrameID()
		}
		a.add(frame, edgeEls(ed, from, to, nil, a.now)...)
		a.st.EdgesAdded++
	}
}

// del marks e and the labels inside it deleted.
func (a *applier) del(e El) bool {
	if e == nil || e.Deleted() {
		return false
	}
	a.set(e, "isDeleted", true)
	a.killed[e.ID()] = true
	for _, t := range a.s.Elements {
		if t.Type() == "text" && t.ContainerID() == e.ID() {
			a.del(t)
		}
	}
	return true
}

func (a *applier) remove(it Item) {
	first := a.byID[it.Elements[0]]
	if first.Deleted() {
		return // already gone with an earlier removal
	}
	switch it.Kind {
	case "node":
		a.del(first)
		for _, e := range a.s.Elements {
			if (e.Type() == "arrow" || e.Type() == "line") && (e.StartID() == first.ID() || e.EndID() == first.ID()) && a.del(e) {
				a.st.EdgesRemoved++
			}
		}
		a.st.NodesRemoved++
	case "edge":
		a.del(first)
		a.st.EdgesRemoved++
	default:
		for _, id := range it.Elements {
			a.del(a.byID[id])
		}
		a.st.OtherRemoved++
	}
}

// unlink drops references to elements this Apply deleted.
func (a *applier) unlink() {
	if len(a.killed) == 0 {
		return
	}
	for _, e := range a.s.Elements {
		if e.Deleted() {
			continue
		}
		if l, ok := e["boundElements"].([]any); ok {
			keep := []any{}
			for _, b := range l {
				if m, _ := b.(map[string]any); m == nil || !a.killed[fmt.Sprint(m["id"])] {
					keep = append(keep, b)
				}
			}
			if len(keep) != len(l) {
				a.set(e, "boundElements", keep)
			}
		}
		for _, k := range []string{"startBinding", "endBinding"} {
			if a.killed[e.binding(k)] {
				a.set(e, k, nil)
			}
		}
		if a.killed[e.FrameID()] {
			a.set(e, "frameId", nil)
		}
	}
}

// flush puts the new elements in the scene: frame members right before
// their frame, the rest at the end.
func (a *applier) flush() error {
	for _, f := range a.order {
		if f == "" {
			continue
		}
		els := a.adds[f]
		i := slices.IndexFunc(a.s.Elements, func(e El) bool { return e.ID() == f })
		if fk := a.s.Elements[i].str("index"); fk != "" {
			prev := ""
			for j := i - 1; j >= 0 && prev == ""; j-- {
				prev = a.s.Elements[j].str("index")
			}
			keys, err := nKeysBetween(prev, fk, len(els))
			if err != nil {
				return err
			}
			for j, e := range els {
				e["index"] = keys[j]
			}
		}
		a.s.Elements = slices.Insert(a.s.Elements, i, els...)
	}
	return a.s.Append(a.adds[""]...)
}

// bbox is the box an element covers; arrows and lines by their points.
func bbox(e El) Box {
	pts, _ := e["points"].([]any)
	if len(pts) == 0 {
		return Box{e.X(), e.Y(), e.W(), e.H()}
	}
	var x0, y0, x1, y1 float64
	for _, p := range pts {
		q, _ := p.([]any)
		if len(q) < 2 {
			continue
		}
		px, _ := q[0].(float64)
		py, _ := q[1].(float64)
		x0, y0, x1, y1 = min(x0, px), min(y0, py), max(x1, px), max(y1, py)
	}
	return Box{e.X() + x0, e.Y() + y0, x1 - x0, y1 - y0}
}

func union(bs []Box) Box {
	if len(bs) == 0 {
		return Box{}
	}
	u := bs[0]
	for _, b := range bs[1:] {
		x0, y0 := min(u.X, b.X), min(u.Y, b.Y)
		u = Box{x0, y0, max(u.X+u.W, b.X+b.W) - x0, max(u.Y+u.H, b.Y+b.H) - y0}
	}
	return u
}

func hits(a, b Box) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

// addNodes draws the nodes and subgraphs the drawing lacks, in groups of
// connected new nodes, and returns the indexes of the edges drawn with them.
func (a *applier) addNodes() map[int]bool {
	g := a.g
	isNewSG := map[string]bool{}
	sgParent := map[string]string{}
	for _, sg := range g.Subgraphs {
		_, ok := a.items[sg.ID]
		isNewSG[sg.ID], sgParent[sg.ID] = !ok, sg.Parent
	}
	// host is the existing subgraph ("" = top level) new content under sg lands in.
	host := func(sg string) string {
		for isNewSG[sg] {
			sg = sgParent[sg]
		}
		return sg
	}
	parent := map[string]string{} // union-find over "n:id" / "s:id"
	var find func(k string) string
	find = func(k string) string {
		if p := parent[k]; p != k {
			parent[k] = find(p)
		}
		return parent[k]
	}
	join := func(x, y string) { parent[find(x)] = find(y) }
	hostOf := map[string]string{}
	var keys []string
	for _, n := range g.Nodes {
		if _, ok := a.items[n.ID]; !ok {
			k := "n:" + n.ID
			parent[k], hostOf[k] = k, host(n.Subgraph)
			keys = append(keys, k)
		}
	}
	for _, sg := range g.Subgraphs {
		if isNewSG[sg.ID] {
			k := "s:" + sg.ID
			parent[k], hostOf[k] = k, host(sg.Parent)
			keys = append(keys, k)
		}
	}
	for _, n := range g.Nodes {
		if _, ok := parent["n:"+n.ID]; ok && isNewSG[n.Subgraph] {
			join("n:"+n.ID, "s:"+n.Subgraph)
		}
	}
	for _, sg := range g.Subgraphs {
		if isNewSG[sg.ID] && isNewSG[sg.Parent] {
			join("s:"+sg.ID, "s:"+sg.Parent)
		}
	}
	for _, ed := range g.Edges {
		f, t := "n:"+ed.From, "n:"+ed.To
		if _, ok := parent[f]; ok {
			if _, ok := parent[t]; ok && hostOf[f] == hostOf[t] {
				join(f, t)
			}
		}
	}
	var roots []string
	members := map[string]map[string]bool{}
	for _, k := range keys {
		r := find(k)
		if members[r] == nil {
			members[r] = map[string]bool{}
			roots = append(roots, r)
		}
		members[r][k] = true
	}
	// Deepest hosts first, so a parent sees its grown children.
	depth := func(h string) int {
		d := 0
		for ; h != ""; h = a.items[h].Parent {
			d++
		}
		return d
	}
	sort.SliceStable(roots, func(i, j int) bool { return depth(hostOf[roots[i]]) > depth(hostOf[roots[j]]) })

	handled := map[int]bool{}
	var placed []Box
	for _, r := range roots {
		m, h := members[r], hostOf[r]
		cg := Graph{Dir: g.Dir}
		for _, n := range g.Nodes {
			if m["n:"+n.ID] {
				if !isNewSG[n.Subgraph] {
					n.Subgraph = ""
				}
				cg.Nodes = append(cg.Nodes, n)
			}
		}
		for _, sg := range g.Subgraphs {
			if m["s:"+sg.ID] {
				if !isNewSG[sg.Parent] {
					sg.Parent = ""
				}
				cg.Subgraphs = append(cg.Subgraphs, sg)
			}
		}
		for i, ed := range g.Edges {
			if m["n:"+ed.From] && m["n:"+ed.To] {
				cg.Edges = append(cg.Edges, ed)
				handled[i] = true
				a.st.EdgesAdded++
			}
		}
		a.st.NodesAdded += len(cg.Nodes)

		els := emitIn(cg, a.now, h == "")
		var bs []Box
		for _, e := range els {
			if e.Type() != "text" {
				bs = append(bs, bbox(e))
			}
		}
		cb := union(bs)
		var x, y float64
		if h == "" {
			x, y = a.spot(cb, m, placed)
		} else {
			x, y = a.inside(h, cb)
		}
		for _, e := range els {
			e["x"], e["y"] = e.X()+x-cb.X, e.Y()+y-cb.Y
			if e.YanaKind() == "node" {
				a.newNode[e.YanaID()] = e
			}
		}
		cb.X, cb.Y = x, y
		placed = append(placed, cb)
		frame := ""
		if h != "" {
			a.grow(h, cb)
			frame = a.hostFrame(h)
		}
		a.add(frame, els...)
	}
	return handled
}

// spot is where a top-level group of new nodes (members m, size of b) goes:
// one rank past the existing nodes it connects to, else past all content,
// at the first place along the cross axis that overlaps nothing.
func (a *applier) spot(b Box, m map[string]bool, placed []Box) (float64, float64) {
	obstacles := append([]Box(nil), placed...)
	for _, e := range a.s.Live() {
		obstacles = append(obstacles, bbox(e))
	}
	var nb []Box
	for _, ed := range a.g.Edges {
		for _, p := range [][2]string{{ed.From, ed.To}, {ed.To, ed.From}} {
			if it, ok := a.items[p[1]]; ok && m["n:"+p[0]] && it.Kind == "node" {
				nb = append(nb, box(a.byID[it.Elements[0]]))
			}
		}
	}
	horiz := a.g.Dir == "LR" || a.g.Dir == "RL"
	if len(nb) > 0 {
		u := union(nb)
		x, y := u.X+u.W/2-b.W/2, u.Y+u.H+rankGap
		switch a.g.Dir {
		case "BT":
			y = u.Y - rankGap - b.H
		case "LR":
			x, y = u.X+u.W+rankGap, u.Y+u.H/2-b.H/2
		case "RL":
			x, y = u.X-rankGap-b.W, u.Y+u.H/2-b.H/2
		}
		for i := 0; i < 100; i++ {
			step := float64((i+1)/2) * 40
			if i%2 == 0 {
				step = -step
			}
			c := Box{x + step, y, b.W, b.H}
			if horiz {
				c = Box{x, y + step, b.W, b.H}
			}
			free := true
			for _, o := range obstacles {
				if hits(Box{c.X - 20, c.Y - 20, c.W + 40, c.H + 40}, o) {
					free = false
					break
				}
			}
			if free {
				return c.X, c.Y
			}
		}
	}
	if len(obstacles) == 0 {
		return 0, 0
	}
	u := union(obstacles)
	if horiz {
		return u.X + u.W + rankGap, u.Y
	}
	return u.X, u.Y + u.H + rankGap
}

func box(e El) Box { return Box{e.X(), e.Y(), e.W(), e.H()} }

// inside is where a group of new nodes goes in existing subgraph h: past its
// current members in the flow direction (right for LR/RL, else below).
// ponytail: ignores edges to existing nodes and may run past h's siblings
// when h grows; anchor and shift like spot if crowded subgraphs need it.
func (a *applier) inside(h string, b Box) (float64, float64) {
	it := a.items[h]
	he := a.byID[it.Elements[0]]
	own := map[string]bool{}
	for _, id := range it.Elements {
		own[id] = true
	}
	var ms []Box
	for _, e := range a.s.Elements {
		if own[e.ID()] || e.Deleted() {
			continue
		}
		cx, cy := e.X()+e.W()/2, e.Y()+e.H()/2
		if he.Type() == "frame" && e.FrameID() == he.ID() ||
			he.Type() != "frame" && cx >= he.X() && cx <= he.X()+he.W() && cy >= he.Y() && cy <= he.Y()+he.H() {
			ms = append(ms, bbox(e))
		}
	}
	for _, els := range a.adds {
		for _, e := range els {
			if e.Type() != "text" && hits(box(e), box(he)) {
				ms = append(ms, bbox(e))
			}
		}
	}
	x, y := he.X()+sgPad, he.Y()+sgPad+sgTitleH
	if len(ms) > 0 {
		u := union(ms)
		if a.g.Dir == "LR" || a.g.Dir == "RL" {
			x = u.X + u.W + rankGap
		} else {
			y = u.Y + u.H + rankGap
		}
	}
	return x, y
}

// grow makes subgraph h and its ancestors big enough to hold b plus padding.
func (a *applier) grow(h string, b Box) {
	for ; h != ""; h = a.items[h].Parent {
		e := a.byID[a.items[h].Elements[0]]
		a.set(e, "width", max(e.W(), b.X+b.W+sgPad-e.X()))
		a.set(e, "height", max(e.H(), b.Y+b.H+sgPad-e.Y()))
		b = box(e)
	}
}

// hostFrame is the element id of the frame holding subgraph h, or "".
func (a *applier) hostFrame(h string) string {
	for ; h != ""; h = a.items[h].Parent {
		if e := a.byID[a.items[h].Elements[0]]; e.Type() == "frame" {
			return e.ID()
		}
	}
	return ""
}
