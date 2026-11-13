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
		} else if i < frameAt && e.YanaID() != "D" && e.ContainerID() != "" && byID[e.ContainerID()].YanaID() != "D" {
			// D is outside the frame; anything else before the frame must be in it
			if e.Type() != "arrow" {
				t.Errorf("element %d (%s) before frame but not in it", i, e.Type())
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
		ed := g.Edges[arrows]
		arrows++
		if e.StartID() != nodeEl[ed.From].ID() || e.EndID() != nodeEl[ed.To].ID() {
			t.Errorf("arrow %d binds wrong nodes", arrows)
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
