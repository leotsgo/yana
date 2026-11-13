package flowchart

import (
	"encoding/json"
	"testing"

	"github.com/leotsgo/yana/internal/drawing"
)

const emitSrc = `flowchart LR
subgraph F[Frame]
  A[Alpha] --> B{Pick}
  subgraph N[Nested]
    C((Round))
  end
  B -.->|maybe| C
end
D[(Store)] ==> A
style A fill:#ff0000,stroke:#00ff00,stroke-width:4px
C --- D
D --> D
`

var required = []string{"id", "type", "x", "y", "width", "height", "angle", "strokeColor", "backgroundColor",
	"fillStyle", "strokeWidth", "strokeStyle", "roughness", "opacity", "groupIds", "frameId", "index",
	"roundness", "seed", "version", "versionNonce", "isDeleted", "boundElements", "updated", "link", "locked"}

func TestNewScene(t *testing.T) {
	g := mustParse(t, emitSrc)
	raw, err := New(g, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := drawing.Validate(raw); err != nil {
		t.Fatal(err)
	}
	s, err := ParseScene(raw)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]El{}
	prev := ""
	for i, e := range s.Elements {
		for _, k := range required {
			if _, ok := e[k]; !ok {
				t.Errorf("element %d (%s) lacks %s", i, e.Type(), k)
			}
		}
		if ix := e.str("index"); ix <= prev {
			t.Errorf("index %q not after %q", ix, prev)
		} else {
			prev = ix
		}
		byID[e.ID()] = e
		switch e.Type() {
		case "text":
			for _, k := range []string{"text", "originalText", "fontSize", "fontFamily", "lineHeight", "textAlign", "verticalAlign", "autoResize", "containerId"} {
				if _, ok := e[k]; !ok {
					t.Errorf("text lacks %s", k)
				}
			}
		case "arrow":
			for _, k := range []string{"points", "startBinding", "endBinding", "startArrowhead", "endArrowhead", "elbowed", "lastCommittedPoint"} {
				if _, ok := e[k]; !ok {
					t.Errorf("arrow lacks %s", k)
				}
			}
		}
	}

	// frame children come right before the frame; frame names/ids
	var frame El
	frameAt := -1
	for i, e := range s.Elements {
		if e.Type() == "frame" {
			frame, frameAt = e, i
		}
	}
	if frame == nil || frame["name"] != "Frame" || frame.YanaID() != "F" || frame.YanaKind() != "subgraph" {
		t.Fatalf("frame = %v", frame)
	}
	inFrame := 0
	for i, e := range s.Elements {
		if e.FrameID() == frame.ID() {
			inFrame++
			if i >= frameAt {
				t.Errorf("child %d after frame", i)
			}
		}
	}
	// frame children are contiguous, right before the frame
	for i := frameAt - 1; i >= frameAt-inFrame; i-- {
		if s.Elements[i].FrameID() != frame.ID() {
			t.Errorf("children not contiguous at %d", i)
		}
	}

	nodeEl := map[string]El{}
	for _, e := range s.Elements {
		if e.YanaKind() == "node" {
			nodeEl[e.YanaID()] = e
			cd := e["customData"].(map[string]any)["yana"].(map[string]any)
			if cd["dir"] != "LR" {
				t.Errorf("dir = %v", cd["dir"])
			}
		}
	}
	for _, n := range g.Nodes {
		e := nodeEl[n.ID]
		if e == nil {
			t.Fatalf("no element for %s", n.ID)
		}
		want := n.ID
		if n.Label != nil {
			want = *n.Label
		}
		ids := e.BoundIDs("text")
		if len(ids) != 1 || byID[ids[0]].Text() != want {
			t.Errorf("%s label: %v want %q", n.ID, ids, want)
		}
		wantFrame := ""
		if n.Subgraph != "" {
			wantFrame = frame.ID()
		}
		if e.FrameID() != wantFrame {
			t.Errorf("%s frameId = %q", n.ID, e.FrameID())
		}
	}
	// shapes
	if nodeEl["B"].Type() != "diamond" || nodeEl["C"].Type() != "ellipse" || nodeEl["D"].Type() != "rectangle" {
		t.Error("shape mapping")
	}
	if nodeEl["A"]["backgroundColor"] != "#ff0000" || nodeEl["A"]["strokeColor"] != "#00ff00" || nodeEl["A"]["strokeWidth"] != 4.0 {
		t.Errorf("style: %v", nodeEl["A"])
	}
	// nested subgraph rectangle
	var nested El
	for _, e := range s.Elements {
		if e.YanaKind() == "subgraph" && e.Type() == "rectangle" {
			nested = e
		}
	}
	if nested == nil || nested["strokeStyle"] != "dashed" || nested["backgroundColor"] != "transparent" || nested.FrameID() != frame.ID() {
		t.Fatalf("nested = %v", nested)
	}
	if tid := nested.BoundIDs("text"); len(tid) != 1 || byID[tid[0]].Text() != "Nested" || byID[tid[0]].str("verticalAlign") != "top" {
		t.Error("nested title")
	}

	// arrows
	arrows := 0
	for _, e := range s.Elements {
		if e.Type() != "arrow" {
			continue
		}
		arrows++
		// match the arrow to its edge through its bindings (emit reorders arrows)
		var ed Edge
		found := false
		for _, c := range g.Edges {
			if e.StartID() == nodeEl[c.From].ID() && e.EndID() == nodeEl[c.To].ID() {
				ed, found = c, true
			}
		}
		if !found {
			t.Fatalf("arrow %d binds no edge", arrows)
		}
		if (ed.Line == "dotted") != (e["strokeStyle"] == "dashed") || (ed.Line == "thick") != (e["strokeWidth"] == 4.0) {
			t.Errorf("arrow %d style", arrows)
		}
		if ed.Arrow != (e["endArrowhead"] == "arrow") {
			t.Errorf("arrow %d head", arrows)
		}
		if ed.Label != "" {
			if ids := e.BoundIDs("text"); len(ids) != 1 || byID[ids[0]].Text() != ed.Label {
				t.Errorf("arrow %d label", arrows)
			}
		}
		pts := e["points"].([]any)
		if ed.From == ed.To && len(pts) < 3 {
			t.Error("self-loop is not a loop")
		}
		if ed.From != ed.To && (e.W() == 0 && e.H() == 0) {
			t.Error("zero-length arrow")
		}
		// arrows inside the frame carry its id, others do not
		inside := (ed.From != "D" && ed.To != "D")
		if inside != (e.FrameID() == frame.ID()) {
			t.Errorf("arrow %d frameId %q", arrows, e.FrameID())
		}
	}
	if arrows != len(g.Edges) {
		t.Errorf("arrows = %d", arrows)
	}
	// the scene is valid JSON with the empty scene's keys
	var top map[string]any
	_ = json.Unmarshal(raw, &top)
	if top["type"] != "excalidraw" || top["source"] != "yana" {
		t.Errorf("top = %v", top)
	}
}

func segHitsBox(ax, ay, bx, by float64, b Box) bool {
	t0, t1 := 0.0, 1.0
	for _, c := range [][3]float64{{-(bx - ax), ax - b.X, 0}, {bx - ax, b.X + b.W - ax, 0}, {-(by - ay), ay - b.Y, 0}, {by - ay, b.Y + b.H - ay, 0}} {
		p, q := c[0], c[1]
		if p == 0 {
			if q <= 0 {
				return false
			}
			continue
		}
		r := q / p
		if p < 0 {
			t0 = max(t0, r)
		} else {
			t1 = min(t1, r)
		}
	}
	return t0 < t1-1e-9
}

func TestArrowsAvoidOtherNodes(t *testing.T) {
	for _, dir := range []string{"TB", "LR"} {
		for _, body := range []string{"A-->B\nB-->C\nA-->C", "A-->B\nB-->C\nC-->A"} {
			g := mustParse(t, "flowchart "+dir+"\n"+body)
			raw, err := New(g, now)
			if err != nil {
				t.Fatal(err)
			}
			s, _ := ParseScene(raw)
			boxes := map[string]Box{}
			for _, e := range s.Elements {
				if e.YanaKind() == "node" {
					boxes[e.ID()] = Box{e.X(), e.Y(), e.W(), e.H()}
				}
			}
			multi := false
			for _, e := range s.Elements {
				if e.Type() != "arrow" {
					continue
				}
				pts := e["points"].([]any)
				if len(pts) > 2 {
					multi = true
				}
				for i := 0; i+1 < len(pts); i++ {
					p, q := pts[i].([]any), pts[i+1].([]any)
					for id, b := range boxes {
						if id == e.StartID() || id == e.EndID() {
							continue
						}
						if segHitsBox(e.X()+p[0].(float64), e.Y()+p[1].(float64), e.X()+q[0].(float64), e.Y()+q[1].(float64), b) {
							t.Errorf("%s %q: arrow segment %d crosses another node", dir, body, i)
						}
					}
				}
			}
			if !multi {
				t.Errorf("%s %q: expected a multi-point arrow", dir, body)
			}
		}
	}
}

func TestUnknownSubgraphGoesTopLevel(t *testing.T) {
	g := mustParse(t, "flowchart TB\nA-->B")
	g.Nodes[0].Subgraph = "ghost"
	nodes, _ := Layout(g)
	if len(nodes) != 2 {
		t.Fatalf("nodes = %v", nodes)
	}
	raw, err := New(g, now)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := ParseScene(raw)
	for _, e := range s.Elements {
		if e.FrameID() != "" {
			t.Error("element placed in a frame")
		}
	}
}
