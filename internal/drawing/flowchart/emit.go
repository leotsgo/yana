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
	if err := s.Append(els...); err != nil {
		return nil, err
	}
	return s.JSON()
}

func shapeElement(n Node, b Box, now time.Time) El {
	typ, round := "rectangle", 0.0
	switch n.Shape {
	case "round", "stadium", "cylinder", "subroutine":
		round = 3
	case "diamond", "hexagon":
		typ, round = "diamond", 2
	case "circle", "double-circle":
		typ, round = "ellipse", 2
	}
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

func emit(g Graph, now time.Time) []El {
	nodes, sgs := Layout(g)
	sgParent := map[string]string{}
	for _, s := range g.Subgraphs {
		sgParent[s.ID] = s.Parent
	}
	// top returns the top-level subgraph containing subgraph id ("" = none).
	top := func(id string) string {
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
		if s.Parent == "" {
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
		if s.Parent == "" {
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

	for _, ed := range g.Edges {
		from, to := byID[ed.From], byID[ed.To]
		if from == nil || to == nil {
			continue
		}
		line := "solid"
		if ed.Line == "dotted" {
			line = "dashed"
		}
		a := newArrow(from, to, ed.Label != "", line, ed.Arrow, now)
		if ed.From == ed.To {
			selfLoop(a, from)
		}
		if ed.Line == "thick" {
			a["strokeWidth"] = 4.0
		}
		els := []El{a}
		if ed.Label != "" {
			els = append(els, newText(a, ed.Label, "middle", now))
		}
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
		if s.Parent != "" {
			continue
		}
		grp := groups[s.ID]
		out = append(out, grp.shapes...)
		out = append(out, grp.arrows...)
		out = append(out, frames[s.ID])
	}
	return append(out, groups[""].arrows...)
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
