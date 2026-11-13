package flowchart

import (
	"fmt"
	"strings"
	"testing"
)

func overlap(a, b Box) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

func TestLayoutNoOverlap(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("flowchart TB\n")
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&sb, "N%d --> N%d\n", i, (i+1)%12) // a cycle
	}
	sb.WriteString("N0 --> N5\nN3 --> N9\nN2 --> N2\nIso\n")
	nodes, _ := Layout(mustParse(t, sb.String()))
	if len(nodes) != 13 {
		t.Fatalf("nodes = %d", len(nodes))
	}
	for a, ba := range nodes {
		for b, bb := range nodes {
			if a < b && overlap(ba, bb) {
				t.Errorf("%s overlaps %s: %v %v", a, b, ba, bb)
			}
		}
	}
}

func TestLayoutSingleAndEmpty(t *testing.T) {
	nodes, sgs := Layout(mustParse(t, "flowchart TB\nA"))
	if len(nodes) != 1 || len(sgs) != 0 || nodes["A"].W != 120 || nodes["A"].H != 60 {
		t.Errorf("%v %v", nodes, sgs)
	}
	nodes, _ = Layout(Graph{Dir: "TB"})
	if len(nodes) != 0 {
		t.Error("empty graph")
	}
}

func TestLayoutDirection(t *testing.T) {
	for dir, check := range map[string]func(a, b Box) bool{
		"TB": func(a, b Box) bool { return b.Y > a.Y+a.H },
		"BT": func(a, b Box) bool { return b.Y+b.H < a.Y },
		"LR": func(a, b Box) bool { return b.X > a.X+a.W },
		"RL": func(a, b Box) bool { return b.X+b.W < a.X },
	} {
		n, _ := Layout(mustParse(t, "flowchart "+dir+"\nA-->B"))
		if !check(n["A"], n["B"]) {
			t.Errorf("%s: A=%v B=%v", dir, n["A"], n["B"])
		}
	}
}

func TestLayoutSubgraphs(t *testing.T) {
	for _, dir := range []string{"TB", "LR"} {
		g := mustParse(t, "flowchart "+dir+`
subgraph O[Outer]
  A --> B
  subgraph I[Inner]
    C --> D
  end
end
subgraph P[Other]
  E
end
X --> A
D --> E
B --> X
`)
		nodes, sgs := Layout(g)
		members := map[string][]string{}
		for _, n := range g.Nodes {
			for s := n.Subgraph; s != ""; {
				members[s] = append(members[s], n.ID)
				for _, sg := range g.Subgraphs {
					if sg.ID == s {
						s = sg.Parent
						break
					}
				}
			}
		}
		for id, sb := range sgs {
			in := map[string]bool{}
			for _, m := range members[id] {
				in[m] = true
				if !contains(sb, nodes[m]) {
					t.Errorf("%s: %s not inside %s: %v %v", dir, m, id, nodes[m], sb)
				}
			}
			for nid, nb := range nodes {
				if !in[nid] && overlap(sb, nb) {
					t.Errorf("%s: non-member %s overlaps %s", dir, nid, id)
				}
			}
		}
		if !contains(sgs["O"], sgs["I"]) {
			t.Errorf("%s: I not inside O", dir)
		}
		if overlap(sgs["O"], sgs["P"]) {
			t.Errorf("%s: O overlaps P", dir)
		}
	}
}

func TestNodeSize(t *testing.T) {
	lbl := ""
	if w, h := nodeSize(Node{ID: "A", Label: &lbl}); w != 120 || h != 60 {
		t.Errorf("empty %v %v", w, h)
	}
	if w, h := nodeSize(Node{ID: "A", Shape: "diamond"}); w != 180 || h != 90 {
		t.Errorf("diamond %v %v", w, h)
	}
	if w, h := nodeSize(Node{ID: "A", Shape: "circle"}); w != 144 || h != 144 {
		t.Errorf("circle %v %v", w, h)
	}
}
