package flowchart

import (
	"time"

	"github.com/leotsgo/yana/internal/drawing"
)

// New is the scene JSON of a new drawing for g (EmptyScene's top-level keys).
func New(g Graph, now time.Time) ([]byte, error) {
	s, err := ParseScene([]byte(drawing.EmptyScene))
	if err != nil {
		return nil, err
	}
	els := emit(g, now)
	for _, e := range els {
		stamp(e)
	}
	if err := s.Append(els...); err != nil {
		return nil, err
	}
	return s.JSON()
}

// primitive is the Excalidraw type a shape token is drawn as, and its
// roundness type (0 = sharp corners).
func primitive(shape string) (typ string, round float64) {
	switch shape {
	case "round", "stadium", "cylinder", "subroutine":
		return "rectangle", 3
	case "diamond", "hexagon":
		return "diamond", 2
	case "circle", "double-circle":
		return "ellipse", 2
	}
	return "rectangle", 0
}

func rounded(e El) bool { r, _ := e["roundness"].(map[string]any); return r != nil }

func shapeElement(n Node, b Box, now time.Time) El {
	typ, round := primitive(n.Shape)
	e := newShape(typ, b.X, b.Y, b.W, b.H, now)
	e["roundness"] = nil
	if round != 0 {
		e["roundness"] = map[string]any{"type": round}
	}
	if n.Stroke != "" {
		e["strokeColor"] = n.Stroke
	}
	if n.Fill != "" {
		e["backgroundColor"] = n.Fill
	}
	if n.StrokeWidth != 0 {
		e["strokeWidth"] = n.StrokeWidth
	}
	return e
}

func emit(g Graph, now time.Time) []El { return emitIn(g, now, true) }

// emitIn is emit; useFrames=false draws every subgraph as a nested rectangle,
// for content that goes inside an existing subgraph.
func emitIn(g Graph, now time.Time, useFrames bool) []El {
	g = normalize(g)
	nodes, sgs, routes := layoutRoutes(g)
	sgParent := map[string]string{}
	for _, s := range g.Subgraphs {
		sgParent[s.ID] = s.Parent
	}
	// top returns the top-level subgraph containing subgraph id ("" = none).
	top := func(id string) string {
		if !useFrames {
			return ""
		}
		for id != "" && sgParent[id] != "" {
			id = sgParent[id]
		}
		return id
	}
	nodeTop := map[string]string{}
	for _, n := range g.Nodes {
		nodeTop[n.ID] = top(n.Subgraph)
	}

	// group collects the elements of one frame ("" = outside any frame).
	type group struct{ shapes, arrows []El }
	groups := map[string]*group{"": {}}
	for _, s := range g.Subgraphs {
		groups[s.ID] = &group{}
	}
	frameID := map[string]string{}
	for _, s := range g.Subgraphs {
		if s.Parent == "" && useFrames {
			frameID[s.ID] = NewID()
		}
	}
	setFrame := func(top string, els ...El) {
		for _, e := range els {
			if top != "" {
				e["frameId"] = frameID[top]
			}
		}
	}

	// Nested subgraph rectangles, parents before children.
	frames := map[string]El{}
	for _, s := range g.Subgraphs {
		b := sgs[s.ID]
		if s.Parent == "" && useFrames {
			f := newFrame(s.Title, b.X, b.Y, b.W, b.H, now)
			f["id"] = frameID[s.ID]
			f["customData"] = map[string]any{"yana": map[string]any{"id": s.ID, "kind": "subgraph"}}
			frames[s.ID] = f
			continue
		}
		r := newShape("rectangle", b.X, b.Y, b.W, b.H, now)
		r["roundness"] = map[string]any{"type": 3.0}
		r["backgroundColor"], r["strokeStyle"] = "transparent", "dashed"
		r["customData"] = map[string]any{"yana": map[string]any{"id": s.ID, "kind": "subgraph"}}
		t := newText(r, s.Title, "top", now)
		setFrame(top(s.ID), r, t)
		grp := groups[top(s.ID)]
		grp.shapes = append(grp.shapes, r, t)
	}

	byID := map[string]El{}
	for _, n := range g.Nodes {
		label := n.ID
		if n.Label != nil {
			label = *n.Label
		}
		shape := n.Shape
		if shape == "" {
			shape = "rect"
		}
		e := shapeElement(n, nodes[n.ID], now)
		e["customData"] = map[string]any{"yana": map[string]any{"id": n.ID, "kind": "node", "shape": shape, "dir": g.Dir}}
		t := newText(e, label, "middle", now)
		setFrame(nodeTop[n.ID], e, t)
		byID[n.ID] = e
		grp := groups[nodeTop[n.ID]]
		grp.shapes = append(grp.shapes, e, t)
	}

	for ei, ed := range g.Edges {
		from, to := byID[ed.From], byID[ed.To]
		if from == nil || to == nil {
			continue
		}
		els := edgeEls(ed, from, to, routes[ei], now)
		gk := ""
		if nodeTop[ed.From] == nodeTop[ed.To] {
			gk = nodeTop[ed.From]
		}
		setFrame(gk, els...)
		groups[gk].arrows = append(groups[gk].arrows, els...)
	}

	// Outside nodes first, then each frame after its children, loose arrows last.
	out := append([]El{}, groups[""].shapes...)
	for _, s := range g.Subgraphs {
		if s.Parent != "" || !useFrames {
			continue
		}
		grp := groups[s.ID]
		out = append(out, grp.shapes...)
		out = append(out, grp.arrows...)
		out = append(out, frames[s.ID])
	}
	return append(out, groups[""].arrows...)
}

// edgeEls is the arrow for ed from from to to, and its label if any. A
// non-empty route (dummy-node centres) bends the arrow through it.
func edgeEls(ed Edge, from, to El, route []pt, now time.Time) []El {
	line := "solid"
	if ed.Line == "dotted" {
		line = "dashed"
	}
	a := newArrow(from, to, ed.Label != "", line, ed.Arrow, now)
	if from.ID() == to.ID() {
		selfLoop(a, from)
	} else if len(route) > 0 {
		throughRoute(a, from, to, route)
	}
	if ed.Line == "thick" {
		a["strokeWidth"] = 4.0
	}
	els := []El{a}
	if ed.Label != "" {
		t := newText(a, ed.Label, "middle", now)
		if pts, _ := a["points"].([]any); len(pts) > 2 { // label on the middle bend
			p, _ := pts[len(pts)/2].([]any)
			t["x"] = a.X() + p[0].(float64) - t.W()/2
			t["y"] = a.Y() + p[1].(float64) - t.H()/2
		}
		els = append(els, t)
	}
	return els
}

// throughRoute makes a an arrow that runs from the border of from, through the
// dummy-node centres, to the border of to, drawn as a smooth curve.
func throughRoute(a, from, to El, route []pt) {
	sx, sy := clip(from, route[0].X, route[0].Y)
	last := route[len(route)-1]
	ex, ey := clip(to, last.X, last.Y)
	all := append(append([]pt{{sx, sy}}, route...), pt{ex, ey})
	minX, minY, maxX, maxY := sx, sy, sx, sy
	pts := make([]any, len(all))
	for i, p := range all {
		pts[i] = []any{p.X - sx, p.Y - sy}
		minX, maxX = min(minX, p.X), max(maxX, p.X)
		minY, maxY = min(minY, p.Y), max(maxY, p.Y)
	}
	a["x"], a["y"], a["width"], a["height"] = sx, sy, maxX-minX, maxY-minY
	a["points"] = pts
	a["roundness"] = map[string]any{"type": 2.0}
}

// selfLoop turns the zero-length arrow of a self-edge into a small loop out of
// and back into the right side of the box. ponytail: the loop sticks 40 out of
// the box and may touch a neighbour; route around siblings if that matters.
func selfLoop(a, box El) {
	x, y := box.X()+box.W(), box.Y()+box.H()*0.25
	h := box.H() / 2
	a["x"], a["y"], a["width"], a["height"] = x, y, 40.0, h
	a["points"] = []any{[]any{0.0, 0.0}, []any{40.0, 0.0}, []any{40.0, h}, []any{0.0, h}}
}
