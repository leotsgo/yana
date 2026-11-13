package flowchart

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	mrand "math/rand/v2"
	"time"
)

// Scene is a parsed scene that keeps every key it does not know.
type Scene struct {
	Top      map[string]json.RawMessage // everything but elements/files
	Elements []El
	Files    map[string]json.RawMessage
}

// El is one element; unknown fields are kept. Numbers are float64.
type El map[string]any

// ParseScene reads an Excalidraw scene.
func ParseScene(sceneJSON []byte) (*Scene, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(sceneJSON, &all); err != nil {
		return nil, fmt.Errorf("scene: %w", err)
	}
	s := &Scene{Top: map[string]json.RawMessage{}, Files: map[string]json.RawMessage{}}
	for k, v := range all {
		switch k {
		case "elements":
			if string(v) != "null" {
				if err := json.Unmarshal(v, &s.Elements); err != nil {
					return nil, fmt.Errorf("scene elements: %w", err)
				}
			}
		case "files":
			if string(v) != "null" {
				if err := json.Unmarshal(v, &s.Files); err != nil {
					return nil, fmt.Errorf("scene files: %w", err)
				}
			}
		default:
			s.Top[k] = v
		}
	}
	return s, nil
}

// JSON serialises the scene, indented like drawing.Merge's output.
func (s *Scene) JSON() ([]byte, error) {
	out := map[string]any{}
	for k, v := range s.Top {
		out[k] = v
	}
	els := s.Elements
	if els == nil {
		els = []El{}
	}
	out["elements"] = els
	files := s.Files
	if files == nil {
		files = map[string]json.RawMessage{}
	}
	out["files"] = files
	return json.MarshalIndent(out, "", "  ")
}

// Live returns the elements that are not deleted.
func (s *Scene) Live() []El {
	var out []El
	for _, e := range s.Elements {
		if !e.Deleted() {
			out = append(out, e)
		}
	}
	return out
}

// ByID returns the element with id (deleted or not), or nil.
func (s *Scene) ByID(id string) El {
	for _, e := range s.Elements {
		if e.ID() == id {
			return e
		}
	}
	return nil
}

// Append adds elements after the last one, giving them fractional indexes
// after the last existing index. A scene with no index at all stays that way.
// On error nothing is changed.
func (s *Scene) Append(els ...El) error {
	last, indexed := "", false
	for _, e := range s.Elements {
		if ix := e.str("index"); ix != "" {
			last, indexed = ix, true
		}
	}
	if indexed || len(s.Elements) == 0 {
		keys, err := nKeysBetween(last, "", len(els))
		if err != nil {
			return err
		}
		for i, e := range els {
			e["index"] = keys[i]
		}
	}
	s.Elements = append(s.Elements, els...)
	return nil
}

func (e El) str(k string) string  { s, _ := e[k].(string); return s }
func (e El) num(k string) float64 { f, _ := e[k].(float64); return f }

func (e El) ID() string          { return e.str("id") }
func (e El) Type() string        { return e.str("type") }
func (e El) X() float64          { return e.num("x") }
func (e El) Y() float64          { return e.num("y") }
func (e El) W() float64          { return e.num("width") }
func (e El) H() float64          { return e.num("height") }
func (e El) Deleted() bool       { b, _ := e["isDeleted"].(bool); return b }
func (e El) ContainerID() string { return e.str("containerId") }
func (e El) FrameID() string     { return e.str("frameId") }
func (e El) Text() string        { return e.str("text") }

// BoundIDs lists the ids in boundElements of the given type.
func (e El) BoundIDs(typ string) []string {
	var out []string
	l, _ := e["boundElements"].([]any)
	for _, b := range l {
		if m, ok := b.(map[string]any); ok && m["type"] == typ {
			if id, _ := m["id"].(string); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

func (e El) binding(k string) string {
	m, _ := e[k].(map[string]any)
	id, _ := m["elementId"].(string)
	return id
}

func (e El) StartID() string { return e.binding("startBinding") }
func (e El) EndID() string   { return e.binding("endBinding") }

func (e El) yana(k string) string {
	cd, _ := e["customData"].(map[string]any)
	y, _ := cd["yana"].(map[string]any)
	s, _ := y[k].(string)
	return s
}

func (e El) YanaID() string    { return e.yana("id") }
func (e El) YanaKind() string  { return e.yana("kind") }
func (e El) YanaShape() string { return e.yana("shape") }

func nonce() float64 { return float64(mrand.Int32N(1<<31-1) + 1) }

// Bump records a change: version+1, new versionNonce, updated=now.
func (e El) Bump(now time.Time) {
	e["version"] = e.num("version") + 1
	e["versionNonce"] = nonce()
	e["updated"] = float64(now.UnixMilli())
}

const b62 = digits

// NewID returns 20 random base62 characters.
func NewID() string {
	b := make([]byte, 20)
	rand.Read(b)
	for i := range b {
		b[i] = b62[int(b[i])%len(b62)] // ponytail: tiny modulo bias, irrelevant for ids
	}
	return string(b)
}

func base(typ string, x, y, w, h float64, now time.Time) El {
	return El{
		"id": NewID(), "type": typ, "x": x, "y": y, "width": w, "height": h, "angle": 0.0,
		"strokeColor": "#1e1e1e", "backgroundColor": "transparent", "fillStyle": "solid",
		"strokeWidth": 2.0, "strokeStyle": "solid", "roughness": 1.0, "opacity": 100.0,
		"groupIds": []any{}, "frameId": nil, "roundness": nil,
		"seed": nonce(), "version": 1.0, "versionNonce": nonce(), "isDeleted": false,
		"boundElements": nil, "updated": float64(now.UnixMilli()), "link": nil, "locked": false,
	}
}

func newShape(typ string, x, y, w, h float64, now time.Time) El {
	e := base(typ, x, y, w, h, now)
	e["customData"] = map[string]any{}
	switch typ {
	case "rectangle", "diamond":
		e["roundness"] = map[string]any{"type": 3.0}
	}
	return e
}

func newFrame(name string, x, y, w, h float64, now time.Time) El {
	e := base("frame", x, y, w, h, now)
	e["name"] = name
	e["customData"] = map[string]any{}
	return e
}

func bind(container El, id string) {
	l, _ := container["boundElements"].([]any)
	container["boundElements"] = append(l, map[string]any{"id": id, "type": "text"})
}

// newText makes text bound inside container (a shape, rectangle or arrow)
// and registers it in the container's boundElements.
func newText(container El, text string, valign string, now time.Time) El {
	w, h := TextSize(text, 20)
	cx, cy := container.X()+container.W()/2, container.Y()+container.H()/2
	if container.Type() == "arrow" {
		pts, _ := container["points"].([]any)
		if len(pts) == 2 {
			p, _ := pts[1].([]any)
			dx, _ := p[0].(float64)
			dy, _ := p[1].(float64)
			cx, cy = container.X()+dx/2, container.Y()+dy/2
		}
	}
	y := cy - h/2
	if valign == "top" {
		y = container.Y() + 8
	}
	e := base("text", cx-w/2, y, w, h, now)
	e["text"], e["originalText"] = text, text
	e["fontSize"], e["fontFamily"], e["lineHeight"] = 20.0, 5.0, 1.25
	e["textAlign"], e["verticalAlign"] = "center", valign
	e["autoResize"], e["containerId"] = true, container.ID()
	bind(container, e.ID())
	return e
}

// clip returns the point where the segment from the centre of b towards (tx,ty) leaves b.
func clip(b El, tx, ty float64) (float64, float64) {
	cx, cy := b.X()+b.W()/2, b.Y()+b.H()/2
	dx, dy := tx-cx, ty-cy
	if dx == 0 && dy == 0 {
		return cx, cy
	}
	t := 1e18
	if dx != 0 {
		t = min(t, (b.W()/2)/abs(dx))
	}
	if dy != 0 {
		t = min(t, (b.H()/2)/abs(dy))
	}
	return cx + dx*t, cy + dy*t
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// newArrow joins from to to, bound at both ends and clipped to the box
// borders. label is for the caller, which adds the text with newText.
// line is the stroke style ("solid", "dashed", "dotted"); arrow=false gives a plain line.
func newArrow(from, to El, label bool, line string, arrow bool, now time.Time) El {
	_ = label
	fcx, fcy := from.X()+from.W()/2, from.Y()+from.H()/2
	tcx, tcy := to.X()+to.W()/2, to.Y()+to.H()/2
	sx, sy := clip(from, tcx, tcy)
	ex, ey := clip(to, fcx, fcy)
	e := base("arrow", sx, sy, abs(ex-sx), abs(ey-sy), now)
	if line != "" {
		e["strokeStyle"] = line
	}
	e["roundness"] = map[string]any{"type": 2.0}
	e["points"] = []any{[]any{0.0, 0.0}, []any{ex - sx, ey - sy}}
	e["startBinding"] = map[string]any{"elementId": from.ID(), "focus": 0.0, "gap": 5.0}
	e["endBinding"] = map[string]any{"elementId": to.ID(), "focus": 0.0, "gap": 5.0}
	e["startArrowhead"] = nil
	e["endArrowhead"] = nil
	if arrow {
		e["endArrowhead"] = "arrow"
	}
	e["elbowed"], e["lastCommittedPoint"] = false, nil
	for _, s := range []El{from, to} {
		l, _ := s["boundElements"].([]any)
		s["boundElements"] = append(l, map[string]any{"id": e.ID(), "type": "arrow"})
	}
	return e
}
