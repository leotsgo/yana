package flowchart

import (
	"strings"
	"testing"
	"time"
)

var now = time.UnixMilli(1700000000000)

func TestSceneRoundTripAndAppend(t *testing.T) {
	in := `{"type":"excalidraw","weird":{"a":1},"elements":[{"id":"a","index":"a0","zzz":[1,2],"version":3}],"files":{}}`
	s, err := ParseScene([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(El{"id": "b"}, El{"id": "c"}); err != nil {
		t.Fatal(err)
	}
	out, _ := s.JSON()
	for _, want := range []string{`"weird"`, `"zzz"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("lost %s", want)
		}
	}
	b, c := s.ByID("b").str("index"), s.ByID("c").str("index")
	if !(b > "a0" && c > b) {
		t.Errorf("indexes %q %q", b, c)
	}
	s2, _ := ParseScene([]byte(`{"elements":[{"id":"a"}]}`))
	_ = s2.Append(El{"id": "b"})
	if _, ok := s2.ByID("b")["index"]; ok {
		t.Error("index added to index-less scene")
	}
}

func TestBump(t *testing.T) {
	e := El{"version": 2.0, "versionNonce": 5.0}
	e.Bump(now)
	if e["version"] != 3.0 || e["versionNonce"] == 5.0 || e["updated"] != 1700000000000.0 {
		t.Errorf("%v", e)
	}
}

func inside(e El, x, y float64) bool {
	return x > e.X() && x < e.X()+e.W() && y > e.Y() && y < e.Y()+e.H()
}

func TestConstructors(t *testing.T) {
	a := newShape("rectangle", 0, 0, 100, 50, now)
	b := newShape("diamond", 300, 100, 100, 50, now)
	txt := newText(a, "hi", "middle", now)
	if txt.ContainerID() != a.ID() || len(a.BoundIDs("text")) != 1 || a.BoundIDs("text")[0] != txt.ID() {
		t.Error("text binding")
	}
	ar := newArrow(a, b, false, "solid", true, now)
	if ar.StartID() != a.ID() || ar.EndID() != b.ID() {
		t.Error("arrow bindings")
	}
	if len(a.BoundIDs("arrow")) != 1 || len(b.BoundIDs("arrow")) != 1 {
		t.Error("shape boundElements")
	}
	pts := ar["points"].([]any)
	end := pts[1].([]any)
	ex, ey := ar.X()+end[0].(float64), ar.Y()+end[1].(float64)
	if inside(a, ar.X(), ar.Y()) || inside(b, ex, ey) {
		t.Error("arrow ends inside boxes")
	}
}

func TestAppendBadIndexChangesNothing(t *testing.T) {
	s, _ := ParseScene([]byte(`{"elements":[{"id":"a","index":"a00"}]}`))
	e := El{"id": "b"}
	if err := s.Append(e); err == nil {
		t.Error("want error")
	}
	if len(s.Elements) != 1 {
		t.Error("scene changed")
	}
	if _, ok := e["index"]; ok {
		t.Error("element mutated")
	}
}

func TestIntegerFieldsAreWhole(t *testing.T) {
	e := newShape("rectangle", 0, 0, 10, 10, now)
	s := &Scene{}
	if err := s.Append(e); err != nil {
		t.Fatal(err)
	}
	out, _ := s.JSON()
	for _, w := range []string{`"version": 1,`, `"updated": 1700000000000,`, `"angle": 0,`} {
		if !strings.Contains(string(out), w) {
			t.Errorf("missing %s", w)
		}
	}
	if strings.Contains(string(out), "e+") || strings.Contains(string(out), `"seed": `+"0.") {
		t.Error("exponent or fraction in output")
	}
}
