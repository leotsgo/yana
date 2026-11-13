package flowchart

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/leotsgo/yana/internal/drawing"
)

var later = now.Add(time.Hour)

func newScene(t *testing.T, src string) []byte {
	t.Helper()
	raw, err := New(mustParse(t, src), now)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mustApply(t *testing.T, scene []byte, src string, remove ...string) ([]byte, Stats) {
	t.Helper()
	out, st, err := Apply(scene, mustParse(t, src), remove, later)
	if err != nil {
		t.Fatalf("Apply(%q): %v", src, err)
	}
	return out, st
}

func mustScene(t *testing.T, raw []byte) *Scene {
	t.Helper()
	s, err := ParseScene(raw)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// yel is the live node or subgraph element with Mermaid id id.
func yel(s *Scene, id string) El {
	for _, e := range s.Live() {
		if e.YanaID() == id && e.Type() != "text" {
			return e
		}
	}
	return nil
}

func label(s *Scene, c El) El {
	for _, id := range c.BoundIDs("text") {
		if t := s.ByID(id); t != nil && !t.Deleted() {
			return t
		}
	}
	return nil
}

// arrowBetween is the live arrow from a to b.
func arrowBetween(s *Scene, a, b El) El {
	for _, e := range s.Live() {
		if e.Type() == "arrow" && e.StartID() == a.ID() && e.EndID() == b.ID() {
			return e
		}
	}
	return nil
}

func editScene(t *testing.T, raw []byte, f func(s *Scene)) []byte {
	t.Helper()
	s := mustScene(t, raw)
	f(s)
	out, err := s.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestApplyIdempotent(t *testing.T) {
	scenes := map[string][]byte{"emit": newScene(t, emitSrc), "hand": handScene()}
	for name, src := range roundTrip {
		scenes[name] = newScene(t, src)
	}
	scenes["noCustomData"] = editScene(t, newScene(t, emitSrc), func(s *Scene) {
		for _, e := range s.Elements {
			delete(e, "customData")
		}
	})
	// the owner turned a stadium into a diamond and squared off a round box
	scenes["ownerShapes"] = editScene(t, newScene(t, "flowchart TB\nA([s]) --> B(r)"), func(s *Scene) {
		yel(s, "A")["type"] = "diamond"
		yel(s, "B")["roundness"] = nil
	})
	for name, raw := range scenes {
		t.Run(name, func(t *testing.T) {
			v, err := Read(raw)
			if err != nil {
				t.Fatal(err)
			}
			out, st := mustApply(t, raw, v.Mermaid())
			if st != (Stats{}) {
				t.Errorf("stats = %+v\n%s", st, v.Mermaid())
			}
			before, after := mustScene(t, raw).Elements, mustScene(t, out).Elements
			if len(before) != len(after) {
				t.Fatalf("elements %d -> %d", len(before), len(after))
			}
			for i := range before {
				if !reflect.DeepEqual(before[i], after[i]) {
					t.Errorf("element %d changed:\n%v\n%v", i, before[i], after[i])
				}
			}
		})
	}
}

func TestApplyKeepsOwnerLayout(t *testing.T) {
	for _, dir := range []string{"TB", "LR"} {
		raw := editScene(t, newScene(t, "flowchart "+dir+"\nA --> B\nB --> C"), func(s *Scene) {
			b := yel(s, "B")
			b["x"] = b.X() + 300
		})
		s0 := mustScene(t, raw)
		out, st := mustApply(t, raw, "flowchart "+dir+"\nB --> N")
		s := mustScene(t, out)
		for _, id := range []string{"A", "B", "C"} {
			if box(yel(s0, id)) != box(yel(s, id)) {
				t.Errorf("%s moved: %v -> %v", id, box(yel(s0, id)), box(yel(s, id)))
			}
		}
		n, b := yel(s, "N"), yel(s, "B")
		if n == nil {
			t.Fatal("no N")
		}
		if l := label(s, n); l == nil || l.Text() != "N" {
			t.Error("N label")
		}
		if arrowBetween(s, b, n) == nil {
			t.Error("no arrow B->N")
		}
		if dir == "TB" && n.Y() < b.Y()+b.H() || dir == "LR" && n.X() < b.X()+b.W() {
			t.Errorf("%s: N %v not past B %v", dir, box(n), box(b))
		}
		for _, e := range s.Live() {
			if e.ID() == n.ID() || e.ContainerID() == n.ID() || e.EndID() == n.ID() {
				continue
			}
			if overlap(box(n), bbox(e)) {
				t.Errorf("%s: N %v overlaps %s %v", dir, box(n), e.Type(), bbox(e))
			}
		}
		if st != (Stats{NodesAdded: 1, EdgesAdded: 1}) {
			t.Errorf("stats = %+v", st)
		}
		if b["version"] != yel(s0, "B")["version"].(float64)+1 {
			t.Error("B not bumped for its new binding")
		}
	}
}

func TestApplyDisconnectedGoesBelow(t *testing.T) {
	raw := newScene(t, "flowchart TB\nA --> B")
	out, _ := mustApply(t, raw, "flowchart TB\nX --> Y")
	s := mustScene(t, out)
	x, b := yel(s, "X"), yel(s, "B")
	if x == nil || yel(s, "Y") == nil || x.Y() < b.Y()+b.H() {
		t.Errorf("X %v B %v", x, b)
	}
}

func TestApplyMissingNodeSurvives(t *testing.T) {
	raw := newScene(t, "flowchart TB\nA --> B --> C")
	out, st := mustApply(t, raw, "flowchart TB\nA --> B")
	if yel(mustScene(t, out), "C") == nil || st != (Stats{}) {
		t.Errorf("C gone or stats %+v", st)
	}
}

func TestApplyRemove(t *testing.T) {
	raw := newScene(t, "flowchart TB\nA --> B --> C\nA -->|x| C")
	s0 := mustScene(t, raw)
	out, st := mustApply(t, raw, "flowchart TB\nA", "C")
	s := mustScene(t, out)
	c0 := yel(s0, "C")
	gone := map[string]bool{c0.ID(): true, label(s0, c0).ID(): true}
	for _, e := range s0.Elements {
		if e.Type() == "arrow" && e.EndID() == c0.ID() {
			gone[e.ID()] = true
			for _, id := range e.BoundIDs("text") {
				gone[id] = true
			}
		}
	}
	if len(gone) != 5 {
		t.Fatalf("setup: %d", len(gone))
	}
	for id := range gone {
		e := s.ByID(id)
		if !e.Deleted() || e["version"] != s0.ByID(id)["version"].(float64)+1 {
			t.Errorf("%s %s not deleted with a bump", e.Type(), id)
		}
	}
	for _, e := range s.Live() {
		for _, typ := range []string{"text", "arrow"} {
			for _, id := range e.BoundIDs(typ) {
				if gone[id] {
					t.Errorf("%s still bound to %s", e.Type(), id)
				}
			}
		}
	}
	if st != (Stats{NodesRemoved: 1, EdgesRemoved: 2}) {
		t.Errorf("stats = %+v", st)
	}

	out, st = mustApply(t, raw, "flowchart TB\nA", "A->B")
	s = mustScene(t, out)
	if arrowBetween(s, yel(s, "A"), yel(s, "B")) != nil || arrowBetween(s, yel(s, "B"), yel(s, "C")) == nil || st != (Stats{EdgesRemoved: 1}) {
		t.Errorf("edge removal: %+v", st)
	}

	// a removal wins over the Mermaid naming the same node or edge
	out, st = mustApply(t, raw, "flowchart TB\nA --> C\nB --> N --> C\nC[new]\nA -->|y| B", "C", "A->B")
	s = mustScene(t, out)
	for _, e := range s.Live() {
		if e.Type() == "arrow" && (e.StartID() == "" || e.EndID() == "") {
			t.Errorf("loose arrow left: %v", e)
		}
		if e.Text() == "new" || e.Text() == "y" {
			t.Errorf("text %q added to a removed item", e.Text())
		}
	}
	if st != (Stats{NodesAdded: 1, EdgesAdded: 1, NodesRemoved: 1, EdgesRemoved: 3}) {
		t.Errorf("stats = %+v", st)
	}

	_, _, err := Apply(raw, mustParse(t, "flowchart TB\nA[changed]"), []string{"nope", "C", "zz"}, later)
	var u *UnknownIDsError
	if !errors.As(err, &u) || err.Error() != "unknown ids: nope, zz" {
		t.Errorf("err = %v", err)
	}
}

func TestApplyRemoveFrameAndFree(t *testing.T) {
	v, _ := Read(handScene())
	var frame, free string
	for _, it := range v.Items {
		if it.Kind == "subgraph" {
			frame = it.ID
		}
		if it.Kind == "free" && it.Type == "image" {
			free = it.ID
		}
	}
	out, st := mustApply(t, handScene(), "flowchart TB", frame, free)
	s := mustScene(t, out)
	if !s.ByID("frameFFFF1").Deleted() || !s.ByID("imgIIII1").Deleted() || s.ByID("rectBBBBBBBB2").Deleted() {
		t.Error("deletions")
	}
	if s.ByID("rectBBBBBBBB2").FrameID() != "" || s.ByID("rectBBBBBBBB2")["version"] != 1.0 {
		t.Errorf("member frameId not cleared with a bump: %v", s.ByID("rectBBBBBBBB2"))
	}
	if st != (Stats{OtherRemoved: 2}) {
		t.Errorf("stats = %+v", st)
	}
}

func TestApplyLabelAndStyle(t *testing.T) {
	raw := newScene(t, "flowchart TB\nA[a] --> B[b]\nB -->|lbl| C")
	s0 := mustScene(t, raw)
	ver := func(s *Scene, e El) float64 { return s.ByID(e.ID())["version"].(float64) }
	a0, b0 := yel(s0, "A"), yel(s0, "B")

	out, st := mustApply(t, raw, "flowchart TB\nA[\"new\"]")
	s := mustScene(t, out)
	if l := label(s, yel(s, "A")); l.Text() != "new" || l["originalText"] != "new" || ver(s, l) != ver(s0, label(s0, a0))+1 {
		t.Errorf("label %v", l)
	}
	if ver(s, a0) != ver(s0, a0) || st != (Stats{NodesUpdated: 1}) {
		t.Errorf("A bumped without growing, stats %+v", st)
	}

	long := strings.Repeat("wide label ", 6)
	out, _ = mustApply(t, raw, "flowchart TB\nA[\""+long+"\"]")
	s = mustScene(t, out)
	if a := yel(s, "A"); a.W() <= a0.W() || a.X() != a0.X() || a.Y() != a0.Y() || ver(s, a) != ver(s0, a0)+1 {
		t.Errorf("A did not grow: %v", box(a))
	}

	out, st = mustApply(t, raw, "flowchart TB\nA")
	if st != (Stats{}) || string(out) != string(mustScene(t, raw).mustJSON(t)) {
		t.Error("bare reference changed something")
	}

	out, _ = mustApply(t, raw, "flowchart TB\nA[\"\"]")
	s = mustScene(t, out)
	if label(s, yel(s, "A")) != nil {
		t.Error("label not cleared")
	}

	out, _ = mustApply(t, raw, "flowchart TB\nA\nstyle A fill:#f00")
	s = mustScene(t, out)
	if yel(s, "A")["backgroundColor"] != "#ff0000" || ver(s, a0) != ver(s0, a0)+1 {
		t.Error("fill")
	}

	// shape change keeps geometry, bindings and text
	out, _ = mustApply(t, raw, "flowchart TB\nA{a}")
	s = mustScene(t, out)
	a := yel(s, "A")
	if a.Type() != "diamond" || a.YanaShape() != "diamond" || box(a) != box(a0) || a.ID() != a0.ID() || !reflect.DeepEqual(a["boundElements"], a0["boundElements"]) {
		t.Errorf("shape change: %v", a)
	}

	out, st = mustApply(t, raw, "flowchart TB\nA -.-> B\nB --> C")
	s = mustScene(t, out)
	ab, bc := arrowBetween(s, a0, b0), arrowBetween(s, b0, yel(s0, "C"))
	if ab["strokeStyle"] != "dashed" || ab["version"] != arrowBetween(s0, a0, b0)["version"].(float64)+1 {
		t.Error("edge line")
	}
	if label(s, bc) != nil {
		t.Error("edge label not cleared")
	}
	if st != (Stats{EdgesUpdated: 2}) {
		t.Errorf("stats = %+v", st)
	}

	out, _ = mustApply(t, raw, "flowchart TB\nA -->|hi| B")
	s = mustScene(t, out)
	if l := label(s, arrowBetween(s, a0, b0)); l == nil || l.Text() != "hi" {
		t.Error("edge label not added")
	}
}

func (s *Scene) mustJSON(t *testing.T) []byte {
	b, err := s.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestApplyHandDrawnIDs(t *testing.T) {
	v, _ := Read(handScene())
	var id string
	for _, it := range v.Items {
		if it.Kind == "node" && it.Elements[0] == "diamDDDDDDDD3" {
			id = it.ID
		}
	}
	out, st := mustApply(t, handScene(), "flowchart TB\n"+id+" --> N[new]")
	s := mustScene(t, out)
	n := yel(s, "N")
	if n == nil || arrowBetween(s, s.ByID("diamDDDDDDDD3"), n) == nil || st != (Stats{NodesAdded: 1, EdgesAdded: 1}) {
		t.Errorf("stats %+v", st)
	}
}

func TestApplyIntoSubgraph(t *testing.T) {
	raw := newScene(t, emitSrc)
	s0 := mustScene(t, raw)
	f0 := yel(s0, "F")
	out, _ := mustApply(t, raw, "flowchart LR\nsubgraph F [Frame]\n  A --> Z[zed]\nend\nsubgraph N [Nested]\n  Q\nend")
	s := mustScene(t, out)
	f, z, q, n := yel(s, "F"), yel(s, "Z"), yel(s, "Q"), yel(s, "N")
	if z == nil || q == nil {
		t.Fatal("nodes not added")
	}
	if !contains(box(f), box(z)) || !contains(box(n), box(q)) || !contains(box(f), box(n)) {
		t.Errorf("not contained: F %v Z %v N %v Q %v", box(f), box(z), box(n), box(q))
	}
	if f.W() < f0.W() || f.H() < f0.H() || f.X() != f0.X() || f.Y() != f0.Y() || f["version"] != f0["version"].(float64)+1 {
		t.Errorf("frame did not grow: %v -> %v", box(f0), box(f))
	}
	idx := map[string]int{}
	for i, e := range s.Elements {
		idx[e.ID()] = i
	}
	prev := ""
	for _, e := range s.Elements {
		if ix := e.str("index"); ix <= prev {
			t.Errorf("index %q not after %q", ix, prev)
		} else {
			prev = ix
		}
	}
	for _, e := range []El{z, q, label(s, z)} {
		if e.FrameID() != f.ID() || idx[e.ID()] > idx[f.ID()] {
			t.Errorf("%s not in frame F", e.Type())
		}
	}
	if err := drawing.Validate(out); err != nil {
		t.Error(err)
	}
}

func TestApplyNewSubgraph(t *testing.T) {
	raw := newScene(t, "flowchart TB\nA --> B")
	out, _ := mustApply(t, raw, "flowchart TB\nsubgraph G [Group]\n  A --> X\n  X --> Y\nend")
	s := mustScene(t, out)
	g, x := yel(s, "G"), yel(s, "X")
	if g == nil || g.Type() != "frame" || g["name"] != "Group" || x.FrameID() != g.ID() || yel(s, "A").FrameID() != "" {
		t.Errorf("frame %v", g)
	}
	if contains(box(g), box(yel(s, "A"))) {
		t.Error("frame wraps an existing node")
	}
}

func TestApplyOldSceneNoIndex(t *testing.T) {
	raw := editScene(t, newScene(t, "flowchart TB\nsubgraph F\n  A --> B\nend"), func(s *Scene) {
		for _, e := range s.Elements {
			delete(e, "index")
		}
	})
	out, _ := mustApply(t, raw, "flowchart TB\nsubgraph F\n  B --> C\nend\nA --> D")
	s := mustScene(t, out)
	for _, e := range s.Elements {
		if _, ok := e["index"]; ok {
			t.Errorf("%s got an index", e.Type())
		}
	}
	merged, _, err := drawing.Merge(raw, out)
	if err != nil {
		t.Fatal(err)
	}
	var m struct{ Elements []map[string]any }
	_ = json.Unmarshal(merged, &m)
	if len(m.Elements) != len(s.Elements) {
		t.Errorf("merge kept %d of %d", len(m.Elements), len(s.Elements))
	}
}

func TestApplyLeavesInputAlone(t *testing.T) {
	raw := newScene(t, "flowchart TB\nA --> B")
	keep := string(raw)
	mustApply(t, raw, "flowchart TB\nA[x] --> C", "B")
	if string(raw) != keep {
		t.Error("input mutated")
	}
}

// grownMemberOverlaps is every live element outside subgraph sg (other than
// sg's ancestors' boxes) that the new node id or sg's grown box overlaps.
func grownMemberOverlaps(t *testing.T, out []byte, sg, id string) []string {
	t.Helper()
	v, err := Read(out)
	if err != nil {
		t.Fatal(err)
	}
	s := mustScene(t, out)
	parent := map[string]string{}
	for _, it := range v.Items {
		parent[it.ID] = it.Parent
	}
	own := map[string]bool{id: true} // sg, its ancestors, the new node
	for p := sg; p != ""; p = parent[p] {
		own[p] = true
	}
	inside := map[string]bool{}
	for _, it := range v.Items {
		if own[it.ID] || it.Kind == "node" && it.Parent == sg {
			for _, e := range it.Elements {
				inside[e] = true
			}
		}
	}
	host, n := yel(s, sg), yel(s, id)
	if n == nil {
		t.Fatalf("%s not added", id)
	}
	var bad []string
	for _, e := range s.Live() {
		if inside[e.ID()] || e.Type() == "arrow" {
			continue // arrows from members cross the border anyway
		}
		if hits(box(n), bbox(e)) {
			bad = append(bad, id+" on "+e.Type()+" "+e.YanaID()+e.Text())
		}
		if hits(box(host), bbox(e)) {
			bad = append(bad, sg+" over "+e.Type()+" "+e.YanaID()+e.Text())
		}
	}
	return bad
}

func TestApplyIntoSubgraphLeavesNeighboursAlone(t *testing.T) {
	for _, dir := range []string{"LR", "TB", "RL", "BT"} {
		raw := newScene(t, "flowchart "+dir+"\nsubgraph FE [Frontend]\n  UI\nend\nsubgraph BE [Backend]\n  API\nend\nUI --> API")
		out, _ := mustApply(t, raw, "flowchart "+dir+"\nsubgraph FE\n  Cache\nend")
		if bad := grownMemberOverlaps(t, out, "FE", "Cache"); len(bad) > 0 {
			t.Errorf("%s: %v", dir, bad)
		}
		s := mustScene(t, out)
		if c, ui, a := yel(s, "Cache"), yel(s, "UI"), arrowBetween(s, yel(s, "UI"), yel(s, "API")); hits(box(c), bbox(a)) || hits(box(c), box(ui)) {
			t.Errorf("%s: Cache %v on UI %v or its arrow %v", dir, box(c), box(ui), bbox(a))
		}
	}
}

func TestApplyIntoNestedSubgraphKeepsSibling(t *testing.T) {
	raw := newScene(t, "flowchart LR\nsubgraph T [Top]\n  subgraph I [Inner]\n    A\n  end\n  B\nend\nA --> B")
	out, _ := mustApply(t, raw, "flowchart LR\nsubgraph I\n  C\nend")
	v, err := Read(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range v.Items {
		if it.ID == "B" && it.Parent != "T" || it.ID == "C" && it.Parent != "I" {
			t.Errorf("%s in=%s", it.ID, it.Parent)
		}
	}
	if bad := grownMemberOverlaps(t, out, "I", "C"); len(bad) > 0 {
		t.Error(bad)
	}
}

func TestApplyIntoSubgraphNoRoom(t *testing.T) {
	raw := newScene(t, "flowchart LR\nsubgraph FE [Frontend]\n  UI\nend")
	// the owner walled FE in on all four sides
	raw = editScene(t, raw, func(s *Scene) {
		f := yel(s, "FE")
		for _, b := range []Box{
			{f.X() + f.W() + 1, f.Y() - 2000, 4000, 4000}, {f.X() - 4001, f.Y() - 2000, 4000, 4000},
			{f.X() - 2000, f.Y() + f.H() + 1, 4000, 4000}, {f.X() - 2000, f.Y() - 4001, 4000, 4000},
		} {
			s.Elements = append(s.Elements, newShape("rectangle", b.X, b.Y, b.W, b.H, now))
		}
	})
	keep := string(raw)
	_, _, err := Apply(raw, mustParse(t, "flowchart LR\nsubgraph FE\n  Cache\nend"), nil, later)
	if err == nil || err.Error() != "no free room in subgraph FE: the boxes in the way were placed by the user; add the new nodes one per call, or ask the user to make room" {
		t.Errorf("err = %v", err)
	}
	if string(raw) != keep {
		t.Error("input mutated")
	}
}

// An arrow (and its label) crossing past the host is not in the way: the
// host may grow over it.
func TestApplyIntoSubgraphGrowsOverArrows(t *testing.T) {
	for _, dir := range []string{"LR", "TB"} {
		raw := newScene(t, "flowchart "+dir+"\n  subgraph FE [Frontend]\n    UI[Web UI]\n  end\n  subgraph BE [Backend]\n    API[API server]\n  end\n  agent([Claude via MCP]) -->|Bearer token| UI\n  UI -->|REST over HTTPS| API")
		out, _, err := Apply(raw, mustParse(t, "flowchart "+dir+"\n  subgraph FE\n    Cache[Local cache]\n  end\n  UI --> Cache"), nil, later)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		s := mustScene(t, out)
		fe, c := yel(s, "FE"), yel(s, "Cache")
		if !contains(box(fe), box(c)) {
			t.Errorf("%s: Cache %v outside FE %v", dir, box(c), box(fe))
		}
		for _, e := range s.Live() {
			if e.Type() == "arrow" || e.Type() == "text" && s.ByID(e.ContainerID()) != nil && s.ByID(e.ContainerID()).Type() == "arrow" {
				continue
			}
			if id := e.YanaID(); e.FrameID() != fe.ID() && e.ID() != fe.ID() && hits(box(fe), bbox(e)) {
				t.Errorf("%s: FE %v over %s %s %v", dir, box(fe), e.Type(), id, bbox(e))
			}
		}
	}
}

// A subgraph hemmed in right and below by its neighbours grows up or left.
func TestApplyIntoSubgraphGrowsAnyWay(t *testing.T) {
	cases := map[string]string{
		"A": "subgraph A\n  a\nend\nsubgraph B\n  b\nend\nsubgraph C\n  c\nend\na --> c\nb --> c",
		"I": "subgraph T\n  subgraph I\n    a\n  end\n  subgraph J\n    b\n  end\n  subgraph K\n    c\n  end\n  a --> c\n  b --> c\nend",
	}
	for _, dir := range []string{"LR", "TB"} {
		for host, body := range cases {
			raw := newScene(t, "flowchart "+dir+"\n"+body)
			s0 := mustScene(t, raw)
			out, _, err := Apply(raw, mustParse(t, "flowchart "+dir+"\nsubgraph "+host+"\n  x\nend"), nil, later)
			if err != nil {
				t.Fatalf("%s %s: %v", dir, host, err)
			}
			if bad := grownMemberOverlaps(t, out, host, "x"); len(bad) > 0 {
				t.Errorf("%s %s: %v", dir, host, bad)
			}
			s := mustScene(t, out)
			if !contains(box(yel(s, host)), box(yel(s, "x"))) {
				t.Errorf("%s %s: x %v outside %v", dir, host, box(yel(s, "x")), box(yel(s, host)))
			}
			for _, id := range []string{"a", "b", "c"} {
				if box(yel(s, id)) != box(yel(s0, id)) {
					t.Errorf("%s %s: %s moved", dir, host, id)
				}
			}
			if host == "I" {
				i, tt := yel(s, "I"), yel(s, "T")
				if !contains(box(tt), box(i)) {
					t.Errorf("%s: T %v does not hold I %v", dir, box(tt), box(i))
				}
				// I is a rectangle (T, at the top, a frame): its title rides on its top
				if l := label(s, i); l == nil || l.Y() != i.Y()+8 || l.X() != i.X()+i.W()/2-l.W()/2 {
					t.Errorf("%s: title of I at %v, box %v", dir, box(l), box(i))
				}
			}
		}
	}
}

// placedAt is the placement an agent recorded on e, or nil.
func placedAt(e El) []any {
	cd, _ := e["customData"].(map[string]any)
	y, _ := cd["yana"].(map[string]any)
	l, _ := y["at"].([]any)
	return l
}

func TestAgentPlacementRecorded(t *testing.T) {
	raw := newScene(t, "flowchart LR\nsubgraph F\n  a --> b\nend")
	check := func(what string, e El) {
		t.Helper()
		if e == nil {
			t.Fatalf("%s missing", what)
		}
		if want := []any{e.X(), e.Y(), e.W(), e.H()}; !reflect.DeepEqual(placedAt(e), want) {
			t.Errorf("%s at = %v, want %v", what, placedAt(e), want)
		}
	}
	s := mustScene(t, raw)
	a := yel(s, "a")
	check("node", a)
	check("text", label(s, a))
	check("arrow", arrowBetween(s, a, yel(s, "b")))
	check("frame", yel(s, "F"))

	out, _ := mustApply(t, raw, "flowchart LR\nsubgraph F\n  a --> n\nend")
	s = mustScene(t, out)
	n := yel(s, "n")
	check("new node", n)
	check("new text", label(s, n))
	check("new arrow", arrowBetween(s, yel(s, "a"), n))
	check("grown frame", yel(s, "F"))
}

const pipeline = "subgraph A\n  a\nend\nsubgraph B\n  b\nend\nsubgraph C\n  c\nend\na --> b --> c"

const noRoomPlaced = "no free room in subgraph B: the boxes in the way were placed by the user; add the new nodes one per call, or ask the user to make room"

// absPt is the drawing position of point i of line e (negative i counts from the end).
func absPt(e El, i int) [2]float64 {
	pts := e["points"].([]any)
	if i < 0 {
		i += len(pts)
	}
	p := pts[i].([]any)
	return [2]float64{e.X() + p[0].(float64), e.Y() + p[1].(float64)}
}

// A subgraph with no room left shifts the boxes past it, if no one but an
// agent placed them.
func TestApplyMakesRoom(t *testing.T) {
	// TB needs three new nodes: two fit beside b without moving anything
	for dir, add := range map[string]string{"LR": "x --> y", "TB": "x --> y --> z"} {
		raw := newScene(t, "flowchart "+dir+"\n"+pipeline)
		s0 := mustScene(t, raw)
		out, _, err := Apply(raw, mustParse(t, "flowchart "+dir+"\nsubgraph B\n  "+add+"\nend"), nil, later)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		s := mustScene(t, out)
		for _, id := range []string{"A", "a", "b"} {
			if box(yel(s, id)) != box(yel(s0, id)) {
				t.Errorf("%s: %s moved", dir, id)
			}
		}
		c0, c := yel(s0, "c"), yel(s, "c")
		dx, dy := c.X()-c0.X(), c.Y()-c0.Y()
		if dir == "LR" && (dx <= 0 || dy != 0) || dir == "TB" && (dy <= 0 || dx != 0) {
			t.Errorf("%s: c shifted by %v,%v", dir, dx, dy)
		}
		for what, p := range map[string][2]El{"C": {yel(s0, "C"), yel(s, "C")}, "c label": {label(s0, c0), label(s, c)}} {
			if p[1].X()-p[0].X() != dx || p[1].Y()-p[0].Y() != dy || p[1].W() != p[0].W() || p[1].H() != p[0].H() {
				t.Errorf("%s: %s %v -> %v, want shift %v,%v", dir, what, box(p[0]), box(p[1]), dx, dy)
			}
		}
		end0, end := absPt(arrowBetween(s0, yel(s0, "b"), c0), -1), absPt(arrowBetween(s, yel(s, "b"), c), -1)
		if end[0]-end0[0] != dx || end[1]-end0[1] != dy {
			t.Errorf("%s: b->c ends at %v, was %v", dir, end, end0)
		}
		var solid []El
		for _, e := range s.Live() {
			if e.Type() != "arrow" && e.Type() != "text" {
				solid = append(solid, e)
			}
		}
		for i, e := range solid {
			for _, o := range solid[i+1:] {
				if hits(box(e), box(o)) && !contains(box(e), box(o)) && !contains(box(o), box(e)) {
					t.Errorf("%s: %s %s %v overlaps %s %s %v", dir, e.Type(), e.YanaID(), box(e), o.Type(), o.YanaID(), box(o))
				}
			}
		}
		if err := drawing.Validate(out); err != nil {
			t.Error(err)
		}
	}
}

// Only boxes no one has moved since an agent placed them make room.
func TestApplyMakeRoomRespectsOwner(t *testing.T) {
	cases := map[string]struct {
		edit func(s *Scene)
		ok   bool
	}{
		"owner moved c": {func(s *Scene) { c := yel(s, "c"); c["x"] = c.X() + 10 }, false},
		"owner recoloured c": {func(s *Scene) {
			c := yel(s, "c")
			c["strokeColor"], c["version"] = "#e03131", c.num("version")+1
		}, true},
		"no record": {func(s *Scene) {
			for _, e := range s.Elements {
				if cd, _ := e["customData"].(map[string]any); cd != nil {
					if y, _ := cd["yana"].(map[string]any); y != nil {
						delete(y, "at")
					}
				}
			}
		}, false},
	}
	for name, tc := range cases {
		raw := editScene(t, newScene(t, "flowchart LR\n"+pipeline), tc.edit)
		keep := string(raw)
		_, _, err := Apply(raw, mustParse(t, "flowchart LR\nsubgraph B\n  x --> y\nend"), nil, later)
		if tc.ok && err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if !tc.ok && (err == nil || err.Error() != noRoomPlaced) {
			t.Errorf("%s: err = %v", name, err)
		}
		if string(raw) != keep {
			t.Errorf("%s: input mutated", name)
		}
	}
}
