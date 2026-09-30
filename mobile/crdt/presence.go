package crdt

import (
	"encoding/json"
	"errors"

	ycrdt "github.com/reearth/ygo/crdt"
)

// posID mirrors an ID as the web's JSON serialisation writes it.
type posID struct {
	Client uint64 `json:"client"`
	Clock  uint64 `json:"clock"`
}

// posJSON is the JSON shape a Yjs RelativePosition takes inside an
// awareness state's cursor field: {"type":null,"tname":"body",
// "item":{"client":4242,"clock":3},"assoc":0}. The web's editor writes
// it with JSON.stringify and reads it back with
// createRelativePositionFromJSON, which accepts the fields being absent
// as well, so both forms decode everywhere.
type posJSON struct {
	Type  *posID  `json:"type"`
	Tname *string `json:"tname"`
	Item  *posID  `json:"item"`
	Assoc int     `json:"assoc"`
}

// RelativePositionJSON anchors a cursor to the document so concurrent
// edits elsewhere do not move it, and returns it in the JSON shape the
// web editor's awareness states carry. index is a UTF-16 offset clamped
// to the body; assoc follows the Yjs convention (>= 0 binds to the
// character at the index, < 0 to the one before it; the web editor
// writes 0). A position at or past the end anchors to the type itself
// and keeps resolving to the end as the text grows.
func (y *Doc) RelativePositionJSON(index int64, assoc int64) ([]byte, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.d == nil {
		return nil, ErrClosed
	}
	if index < 0 {
		return nil, errors.New("yana/crdt: index must be >= 0")
	}
	i, err := toIndex(index)
	if err != nil {
		return nil, err
	}
	if l := int(y.text.Len()); i > l {
		i = l
	}
	rp := ycrdt.CreateRelativePositionFromIndex(y.text, i, int(assoc))
	name := TextName
	return json.Marshal(posJSON{Tname: &name, Item: toPosID(rp.Item), Assoc: rp.Assoc})
}

// ResolveRelativePositionJSON resolves a position in the web's JSON
// shape against the current text and returns its UTF-16 index, or -1
// when it cannot be resolved: the anchor's item never existed here or
// is gone, or the position binds to a nested type this package does not
// open.
func (y *Doc) ResolveRelativePositionJSON(data []byte) int64 {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.d == nil {
		return -1
	}
	var p posJSON
	if err := json.Unmarshal(data, &p); err != nil {
		return -1
	}
	if p.Type != nil {
		// A position inside a nested type. The body is a root type, so
		// a peer's cursor should never carry one; refuse rather than
		// mis-resolve it.
		return -1
	}
	rp := ycrdt.RelativePosition{Item: toID(p.Item), Assoc: p.Assoc}
	if p.Tname != nil {
		rp.Tname = *p.Tname
	}
	pos, ok := ycrdt.ToAbsolutePosition(y.d, rp)
	if !ok {
		return -1
	}
	return int64(pos.Index)
}

func toPosID(id *ycrdt.ID) *posID {
	if id == nil {
		return nil
	}
	return &posID{Client: uint64(id.Client), Clock: id.Clock}
}

func toID(id *posID) *ycrdt.ID {
	if id == nil {
		return nil
	}
	return &ycrdt.ID{Client: ycrdt.ClientID(id.Client), Clock: id.Clock}
}
