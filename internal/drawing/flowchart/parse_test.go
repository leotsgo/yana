package flowchart

import (
	"errors"
	"reflect"
	"testing"
)

func mustParse(t *testing.T, src string) Graph {
	t.Helper()
	g, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return g
}

func lbl(g Graph, id string) string {
	for _, n := range g.Nodes {
		if n.ID == id {
			if n.Label == nil {
				return "<nil>"
			}
			return *n.Label
		}
	}
	return "<missing>"
}

func node(g Graph, id string) Node {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	return Node{}
}

func TestParseHeader(t *testing.T) {
	for src, dir := range map[string]string{"flowchart LR": "LR", "graph TD": "TB", "graph": "TB", "flowchart BT;": "BT", "%% hi\n\nflowchart RL": "RL"} {
		if g := mustParse(t, src); g.Dir != dir {
			t.Errorf("%q: Dir %q want %q", src, g.Dir, dir)
		}
	}
	for _, src := range []string{"sequenceDiagram\nA->>B: hi", "", "%% only comment"} {
		if _, err := Parse(src); !errors.Is(err, ErrNotFlowchart) {
			t.Errorf("%q: err %v, want ErrNotFlowchart", src, err)
		}
	}
}

func TestParseShapes(t *testing.T) {
	for shape, src := range map[string]string{
		"rect": `A[x]`, "round": `A(x)`, "diamond": `A{x}`, "circle": `A((x))`, "double-circle": `A(((x)))`,
		"stadium": `A([x])`, "cylinder": `A[(x)]`, "subroutine": `A[[x]]`, "hexagon": `A{{x}}`,
		"parallelogram": `A[/x/]`, "parallelogram-alt": `A[\x\]`, "trapezoid": `A[/x\]`, "trapezoid-alt": `A[\x/]`,
		"asymmetric": `A>x]`,
	} {
		g := mustParse(t, "flowchart TB\n"+src)
		n := node(g, "A")
		if n.Shape != shape || n.Label == nil || *n.Label != "x" {
			t.Errorf("%s: got shape %q label %q", src, n.Shape, lbl(g, "A"))
		}
	}
	n := node(mustParse(t, "flowchart TB\nA"), "A")
	if n.Label != nil || n.Shape != "" {
		t.Errorf("bare A: %+v", n)
	}
	if g := mustParse(t, "flowchart TB\nA[\"\"]"); lbl(g, "A") != "" || node(g, "A").Shape != "rect" {
		t.Errorf("A[\"\"] must be given-empty")
	}
	g := mustParse(t, "flowchart TB\nA[One]\nA --> B\nA[Two]")
	if lbl(g, "A") != "Two" || len(g.Nodes) != 2 {
		t.Errorf("last label wins: %q, %d nodes", lbl(g, "A"), len(g.Nodes))
	}
}

func TestParseLabels(t *testing.T) {
	for src, want := range map[string]string{
		`A["say \"hi\" | [x]"]`: `say "hi" | [x]`,
		`A["a#quot;b"]`:         `a"b`,
		`A["x<br>y"]`:           "x\ny",
		`A[x<br/>y]`:            "x\ny",
		`A["café ✓"]`:           "café ✓",
		"A[\"`**b**`\"]":        "**b**",
		`A["a;b %% c"]`:         "a;b %% c",
		`A[  spaced  ]`:         "spaced",
	} {
		if got := lbl(mustParse(t, "flowchart TB\n"+src), "A"); got != want {
			t.Errorf("%s: %q want %q", src, got, want)
		}
	}
	// unescaped inner quotes
	if got := lbl(mustParse(t, "flowchart TB\n"+`A["say "hi" | [x]"]`), "A"); got != `say "hi" | [x]` {
		t.Errorf("raw quotes: %q", got)
	}
}

func TestParseEdges(t *testing.T) {
	for src, want := range map[string]Edge{
		"A --> B":          {"A", "B", "", "solid", true},
		"A --- B":          {"A", "B", "", "solid", false},
		"A-->B":            {"A", "B", "", "solid", true},
		"A -.-> B":         {"A", "B", "", "dotted", true},
		"A -.- B":          {"A", "B", "", "dotted", false},
		"A ==> B":          {"A", "B", "", "thick", true},
		"A === B":          {"A", "B", "", "thick", false},
		"A -->|go| B":      {"A", "B", "go", "solid", true},
		`A -->|"go on"| B`: {"A", "B", "go on", "solid", true},
		"A -- go --> B":    {"A", "B", "go", "solid", true},
		"A -- go --- B":    {"A", "B", "go", "solid", false},
		"A -. go .-> B":    {"A", "B", "go", "dotted", true},
		"A -. go .- B":     {"A", "B", "go", "dotted", false},
		"A == go ==> B":    {"A", "B", "go", "thick", true},
		"A ---|go| B":      {"A", "B", "go", "solid", false},
	} {
		g := mustParse(t, "flowchart TB\n"+src)
		if len(g.Edges) != 1 || g.Edges[0] != want {
			t.Errorf("%s: %+v want %+v", src, g.Edges, want)
		}
	}
	if g := mustParse(t, "flowchart TB\nA --> B --> C"); len(g.Edges) != 2 || g.Edges[1].From != "B" || g.Edges[1].To != "C" {
		t.Errorf("chain: %+v", g.Edges)
	}
	g := mustParse(t, "flowchart TB\nA & B --> C & D")
	if len(g.Edges) != 4 || g.Edges[0] != (Edge{"A", "C", "", "solid", true}) || g.Edges[3] != (Edge{"B", "D", "", "solid", true}) {
		t.Errorf("fan: %+v", g.Edges)
	}
	g = mustParse(t, "flowchart TB\nA[One] --> B{Two}")
	if lbl(g, "A") != "One" || node(g, "B").Shape != "diamond" || lbl(g, "B") != "Two" {
		t.Errorf("inline defs: %+v", g.Nodes)
	}
}

func TestParseSubgraphs(t *testing.T) {
	g := mustParse(t, "flowchart TB\nsubgraph pod [yana pod]\nA\nend\nsubgraph p2[\"two words\"]\nB\nend\nsubgraph \"Just title\"\nC\nend")
	want := []Subgraph{{"pod", "yana pod", ""}, {"p2", "two words", ""}, {"Just_title", "Just title", ""}}
	if !reflect.DeepEqual(g.Subgraphs, want) {
		t.Errorf("subgraphs: %+v", g.Subgraphs)
	}
	if node(g, "A").Subgraph != "pod" || node(g, "C").Subgraph != "Just_title" {
		t.Errorf("membership: %+v", g.Nodes)
	}

	g = mustParse(t, "flowchart TB\nsubgraph outer\ndirection LR\nsubgraph inner\nX\nend\nY\nend\nsubgraph s3\nend")
	if g.Subgraphs[1] != (Subgraph{"inner", "inner", "outer"}) || g.Subgraphs[2].Parent != "" {
		t.Errorf("nesting: %+v", g.Subgraphs)
	}
	if node(g, "X").Subgraph != "inner" || node(g, "Y").Subgraph != "outer" {
		t.Errorf("membership: %+v", g.Nodes)
	}

	g = mustParse(t, "flowchart TB\nA[Top]\nsubgraph s\nA\nend")
	if node(g, "A").Subgraph != "s" || lbl(g, "A") != "Top" {
		t.Errorf("top then listed: %+v", g.Nodes)
	}
}

func TestParseStyles(t *testing.T) {
	g := mustParse(t, "flowchart TB\nA\nstyle A fill:#f9f,stroke:#333,stroke-width:4px")
	if n := node(g, "A"); n.Fill != "#ff99ff" || n.Stroke != "#333333" || n.StrokeWidth != 4 {
		t.Errorf("style: %+v", n)
	}
	g = mustParse(t, "flowchart TB\nA\nB\nC:::hot\nclass A,B hot\nclassDef hot fill:#f00,stroke:#ABCDEF")
	for _, id := range []string{"A", "B", "C"} {
		if n := node(g, id); n.Fill != "#ff0000" || n.Stroke != "#ABCDEF" {
			t.Errorf("%s: %+v", id, n)
		}
	}
	g = mustParse(t, "flowchart TB\nstyle A fill:red\nA:::c\nclassDef c fill:#00f")
	if node(g, "A").Fill != "red" {
		t.Errorf("style beats class: %+v", node(g, "A"))
	}
}

func TestParseIgnored(t *testing.T) {
	g := mustParse(t, "flowchart TB\n%% comment\nA --> B %% trailing\nclick A callback\nlinkStyle 0 stroke:#f00\nC --> D; E --> F;")
	if len(g.Nodes) != 6 || len(g.Edges) != 3 {
		t.Errorf("nodes %d edges %d", len(g.Nodes), len(g.Edges))
	}
}

func TestParseErrors(t *testing.T) {
	for src, want := range map[string]string{
		"flowchart TB\nA --> B\nC[open":                 "line 3: unclosed [ in node C",
		"flowchart TB\nA((x)":                           "line 2: unclosed (( in node A",
		"flowchart TB\nA[\"x]":                          "line 2: unclosed [ in node A",
		"flowchart TB\nend":                             "line 2: end without a subgraph",
		"flowchart TB\nsubgraph s\nA":                   "line 2: subgraph s is never closed with end",
		"flowchart TB\nA --> B bogus":                   "",
		"flowchart TB\nA -->":                           "",
		"flowchart TB\n???":                             "",
		"flowchart XX":                                  "",
		"flowchart TB\nA\nclass A nope":                 "line 3: unknown class nope",
		"flowchart TB\nA:::nope":                        "line 2: unknown class nope",
		"flowchart TB\nstyle Z fill:#fff":               "line 2: unknown node Z",
		"flowchart TB\nclassDef c fill:#fff\nclass Z c": "line 3: unknown node Z",
	} {
		_, err := Parse(src)
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Errorf("%q: err %v, want *ParseError", src, err)
			continue
		}
		if want != "" && err.Error() != want {
			t.Errorf("%q: %q want %q", src, err.Error(), want)
		}
	}
}
