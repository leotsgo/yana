package flowchart

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// canon makes a Graph comparable: defaults filled in, edges sorted.
func canon(g Graph) []string {
	var out []string
	out = append(out, "dir "+g.Dir)
	for _, n := range g.Nodes {
		l, sh := n.ID, n.Shape
		if n.Label != nil {
			l = *n.Label
		}
		if sh == "" {
			sh = "rect"
		}
		w := n.StrokeWidth
		if w == 2 {
			w = 0
		}
		out = append(out, fmt.Sprintf("node %s %q %s in=%s s=%s f=%s w=%v", n.ID, l, sh, n.Subgraph, n.Stroke, n.Fill, w))
	}
	for _, e := range g.Edges {
		out = append(out, fmt.Sprintf("edge %s %s %q %s %v", e.From, e.To, e.Label, e.Line, e.Arrow))
	}
	for _, s := range g.Subgraphs {
		out = append(out, fmt.Sprintf("sub %s %q %s", s.ID, s.Title, s.Parent))
	}
	sort.Strings(out)
	return out
}

var roundTrip = map[string]string{
	"simple": "flowchart LR\nA[Start] --> B[End]\n",
	"shapes": `flowchart TB
A[Rect] --> B(Round) --> C{Diamond} --> D((Circle))
D -.-> E([Stadium]) ==> F[(DB)]
F --- G[[Sub]] === H{{Hex}}
H -->|yes| I[/Para/]
I --> J[\Alt\]
J --> K>Flag]
K --> L(((Double)))
`,
	"nested": `flowchart TB
subgraph Outer [Outer title]
  A[a] --> B[b]
  subgraph Inner [Inner]
    C[c]
    subgraph Deep [Deep]
      D[d]
    end
  end
end
B --> C
C --> D
E[e] --> A
`,
	"styled": "flowchart TB\nA[a] --> B[b]\nstyle A fill:#ff0000,stroke:#00ff00,stroke-width:4px\nstyle B fill:#abc\n",
	"labels": "flowchart TB\n" +
		`A["say #quot;hi#quot; | there"] -->|"a|b #quot;q#quot;"| B["ünï ☃ <br>two#35;lines #amp; #lt;br#gt; back\#92;slash"]` + "\n" +
		`B -->|"x; y %% z"| C["` + "`" + `tick` + "`" + `"]` + "\n" +
		`C ---|"multi<br>line"| A` + "\n",
}

func TestReadRoundTrip(t *testing.T) {
	for name, src := range roundTrip {
		t.Run(name, func(t *testing.T) {
			g := mustParse(t, src)
			raw, err := New(g, now)
			if err != nil {
				t.Fatal(err)
			}
			v, err := Read(raw)
			if err != nil {
				t.Fatal(err)
			}
			g2, err := Parse(v.Mermaid())
			if err != nil {
				t.Fatalf("Parse(%q): %v", v.Mermaid(), err)
			}
			if want, got := canon(g), canon(g2); !reflect.DeepEqual(want, got) {
				t.Errorf("mermaid:\n%s\nwant %q\ngot  %q", v.Mermaid(), want, got)
			}
			if want, got := canon(g), canon(v.Graph); !reflect.DeepEqual(want, got) {
				t.Errorf("view.Graph\nwant %q\ngot  %q", want, got)
			}
			if !strings.Contains(v.Text(), "\n\nlayout:\n") {
				t.Errorf("no layout section: %s", v.Text())
			}
		})
	}
}

func handScene() []byte {
	el := func(id, typ string, x, y, w, h float64, extra map[string]any) map[string]any {
		m := map[string]any{"id": id, "type": typ, "x": x, "y": y, "width": w, "height": h,
			"strokeColor": "#1e1e1e", "backgroundColor": "transparent", "strokeWidth": 2, "strokeStyle": "solid",
			"isDeleted": false, "frameId": nil, "roundness": nil, "boundElements": nil}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	bound := func(l ...string) []any {
		var o []any
		for i := 0; i < len(l); i += 2 {
			o = append(o, map[string]any{"id": l[i], "type": l[i+1]})
		}
		return o
	}
	els := []any{
		el("rectAAAAAAAA1", "rectangle", 0, 0, 100, 60, map[string]any{"boundElements": bound("txtA", "text", "arr1", "arrow"), "roundness": map[string]any{"type": 3}, "backgroundColor": "#ffc9c9"}),
		el("txtA", "text", 10, 20, 80, 25, map[string]any{"text": "Alpha", "containerId": "rectAAAAAAAA1"}),
		el("rectBBBBBBBB2", "rectangle", 300, 0, 100, 60, map[string]any{"boundElements": bound("txtB", "text", "arr1", "arrow"), "frameId": "frameFFFF1"}),
		el("txtB", "text", 310, 20, 80, 25, map[string]any{"text": "Beta", "containerId": "rectBBBBBBBB2"}),
		el("diamDDDDDDDD3", "diamond", 150, 200, 80, 80, nil),
		el("arr1", "arrow", 100, 30, 200, 0, map[string]any{"startBinding": map[string]any{"elementId": "rectAAAAAAAA1"}, "endBinding": map[string]any{"elementId": "rectBBBBBBBB2"}, "endArrowhead": "arrow", "strokeStyle": "dashed", "boundElements": bound("arrLbl", "text")}),
		el("arrLbl", "text", 180, 10, 40, 25, map[string]any{"text": "go", "containerId": "arr1"}),
		el("arrLoose", "arrow", 0, 400, 50, 0, map[string]any{"startBinding": map[string]any{"elementId": "rectAAAAAAAA1"}, "endBinding": nil, "endArrowhead": "arrow"}),
		el("freeTxt1", "text", 0, 500, 60, 25, map[string]any{"text": "note", "containerId": nil}),
		el("frameFFFF1", "frame", 280, -20, 140, 100, map[string]any{"name": "Group"}),
		el("imgIIII1", "image", 500, 0, 50, 50, map[string]any{"fileId": "f1"}),
		el("gone1", "rectangle", 700, 0, 50, 50, map[string]any{"isDeleted": true}),
	}
	b, _ := json.Marshal(map[string]any{"type": "excalidraw", "elements": els, "files": map[string]any{}})
	return b
}

func TestReadHandDrawn(t *testing.T) {
	v, err := Read(handScene())
	if err != nil {
		t.Fatal(err)
	}
	v2, _ := Read(handScene())
	if v.Text() != v2.Text() {
		t.Errorf("ids not stable:\n%s\n%s", v.Text(), v2.Text())
	}
	kinds := map[string]int{}
	for _, it := range v.Items {
		kinds[it.Kind]++
	}
	if kinds["node"] != 3 || kinds["edge"] != 1 || kinds["subgraph"] != 1 || kinds["free"] != 3 {
		t.Errorf("kinds = %v\n%s", kinds, v.Text())
	}
	if len(v.Graph.Nodes) != 3 || len(v.Graph.Edges) != 1 || len(v.Graph.Subgraphs) != 1 {
		t.Fatalf("graph = %+v", v.Graph)
	}
	for _, n := range v.Graph.Nodes {
		if !strings.HasPrefix(n.ID, "x") || n.Label == nil {
			t.Errorf("node %+v", n)
		}
	}
	a, b := v.Graph.Nodes[0], v.Graph.Nodes[1]
	if *a.Label != "Alpha" || a.Shape != "round" || a.Fill != "#ffc9c9" || a.Stroke != "" {
		t.Errorf("a = %+v", a)
	}
	if v.Graph.Nodes[2].Shape != "diamond" || *v.Graph.Nodes[2].Label != "" {
		t.Errorf("diamond = %+v", v.Graph.Nodes[2])
	}
	if b.Subgraph != v.Graph.Subgraphs[0].ID || v.Graph.Subgraphs[0].Title != "Group" {
		t.Errorf("b = %+v sg = %+v", b, v.Graph.Subgraphs)
	}
	e := v.Graph.Edges[0]
	if e.From != a.ID || e.To != b.ID || e.Label != "go" || e.Line != "dotted" || !e.Arrow {
		t.Errorf("edge = %+v", e)
	}
	for _, it := range v.Items {
		if it.Kind == "edge" && len(it.Elements) != 2 {
			t.Errorf("edge elements %v", it.Elements)
		}
		if it.Kind == "free" && it.Type == "arrow" && (it.From != a.ID || it.To != "") {
			t.Errorf("loose arrow %+v", it)
		}
		if it.Kind == "free" && it.Type == "image" && it.FileID != "f1" {
			t.Errorf("image %+v", it)
		}
	}
	if _, err := Parse(v.Mermaid()); err != nil {
		t.Errorf("mermaid: %v\n%s", err, v.Mermaid())
	}
	if !strings.Contains(v.Text(), "free ") || !strings.Contains(v.Text(), `text="note"`) {
		t.Errorf("text:\n%s", v.Text())
	}
}

func TestReadSkipsDeleted(t *testing.T) {
	v, _ := Read(handScene())
	for _, it := range v.Items {
		for _, id := range it.Elements {
			if id == "gone1" {
				t.Errorf("deleted element listed: %+v", it)
			}
		}
	}
	if len(v.Items) != 8 {
		t.Errorf("items = %d\n%s", len(v.Items), v.Text())
	}
}

func TestReadShapeFollowsOwnerType(t *testing.T) {
	raw := newScene(t, "flowchart TB\nA([s]) --> B(r) --> C[(db)]")
	s := mustScene(t, raw)
	yel(s, "A")["type"] = "diamond"
	yel(s, "B")["roundness"] = nil
	raw, _ = s.JSON()
	v, err := Read(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, n := range v.Graph.Nodes {
		got[n.ID] = n.Shape
	}
	if want := map[string]string{"A": "diamond", "B": "rect", "C": "cylinder"}; !reflect.DeepEqual(got, want) {
		t.Errorf("shapes = %v", got)
	}
}

// TestReadLinks: a link on any element shows on its layout line, quoted
// when it holds spaces; an omitted item says so.
func TestReadLinks(t *testing.T) {
	scene := `{"type":"excalidraw","elements":[` +
		`{"id":"n1","type":"rectangle","x":0,"y":0,"width":100,"height":50,"link":"[[Target]]"},` +
		`{"id":"n2","type":"rectangle","x":300,"y":0,"width":100,"height":50,"link":null},` +
		`{"id":"a1","type":"arrow","x":100,"y":25,"width":200,"height":0,"link":"https://x.example/a b",` +
		`"startBinding":{"elementId":"n1"},"endBinding":{"elementId":"n2"},"endArrowhead":"arrow"},` +
		`{"id":"f1","type":"frame","x":-50,"y":-50,"width":600,"height":200,"name":"G","link":"[[Group]]"},` +
		`{"id":"t1","type":"text","x":0,"y":300,"width":50,"height":20,"text":"hi","link":"[[My Note]]"},` +
		`{"id":"i1","type":"image","x":0,"y":400,"width":50,"height":50,"fileId":"f"}` +
		`],"files":{}}`
	v, err := Read([]byte(scene))
	if err != nil {
		t.Fatal(err)
	}
	for i := range v.Items {
		if v.Items[i].Type == "image" {
			v.Items[i].Omitted = true
		}
	}
	txt := v.Text()
	lines := map[string]string{}
	for _, l := range strings.Split(txt, "\n") {
		f := strings.Fields(l)
		if len(f) > 1 {
			lines[f[0]+" "+f[1]] = l
		}
	}
	for key, want := range map[string]string{
		"node " + v.Items[0].ID:     " link=[[Target]]",
		"edge " + v.Items[2].ID:     ` link="https://x.example/a b"`,
		"subgraph " + v.Items[3].ID: " link=[[Group]]",
		"free " + v.Items[4].ID:     ` link="[[My Note]]"`,
		"free " + v.Items[5].ID:     " image (not included)",
	} {
		if !strings.HasSuffix(lines[key], want) {
			t.Errorf("%s: want suffix %q in\n%s", key, want, txt)
		}
	}
	if strings.Contains(lines["node "+v.Items[1].ID], "link=") {
		t.Errorf("a null link shows:\n%s", txt)
	}
}
