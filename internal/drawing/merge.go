package drawing

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// ErrIncoming means the scene sent to Merge is not one it can read; a
// scene on disk that cannot be read is any other error.
var ErrIncoming = errors.New("drawing: incoming scene cannot be merged")

// stamp is what the merge reads of an element; the element itself is kept
// as raw JSON so no field yana does not know about is lost.
type stamp struct {
	ID           string  `json:"id"`
	Version      float64 `json:"version"`
	VersionNonce float64 `json:"versionNonce"`
	IsDeleted    bool    `json:"isDeleted"`
	Index        string  `json:"index"`
	FileID       string  `json:"fileId"`
}

type element struct {
	stamp
	raw json.RawMessage
}

// Merge folds the scene on disk into an incoming scene, element by element,
// with Excalidraw's own reconciliation rule: per id the higher version wins,
// and of two equal versions the lower versionNonce. A delete is an element
// with isDeleted set, so it wins like any other edit when it is newer.
// Elements only one side has are kept. The incoming scene's appState and
// other keys are kept; files are the union of both, limited to the ones a
// live element points at.
//
// changed is false when the disk adds nothing the incoming scene lacks;
// the caller then writes incoming as sent. Both scenes must be valid
// (Validate); an element without an id is kept from incoming only.
func Merge(disk, incoming []byte) (merged []byte, changed bool, err error) {
	var in, cur map[string]json.RawMessage
	if err := json.Unmarshal(incoming, &in); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrIncoming, err)
	}
	inEls, err := elements(in["elements"])
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrIncoming, err)
	}
	inFiles := map[string]json.RawMessage{}
	if raw := in["files"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &inFiles); err != nil {
			return nil, false, fmt.Errorf("%w: files: %v", ErrIncoming, err)
		}
	}
	if err := json.Unmarshal(disk, &cur); err != nil {
		return nil, false, fmt.Errorf("drawing: bad scene on disk: %w", err)
	}
	curEls, err := elements(cur["elements"])
	if err != nil {
		return nil, false, err
	}

	pos := map[string]int{}
	out := make([]element, 0, len(inEls))
	for _, e := range inEls {
		if e.ID != "" {
			if _, dup := pos[e.ID]; dup {
				continue
			}
			pos[e.ID] = len(out)
		}
		out = append(out, e)
	}
	for _, d := range curEls {
		if d.ID == "" {
			continue
		}
		i, ok := pos[d.ID]
		if !ok {
			pos[d.ID] = len(out)
			out = append(out, d)
			changed = true
			continue
		}
		if newer(d.stamp, out[i].stamp) {
			out[i] = d
			changed = true
		}
	}
	if ordered(out) {
		sort.SliceStable(out, func(a, b int) bool {
			if out[a].Index != out[b].Index {
				return out[a].Index < out[b].Index
			}
			return out[a].ID < out[b].ID
		})
	}

	curFiles := map[string]json.RawMessage{}
	if raw := cur["files"]; len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &curFiles) // unreadable files on disk are not worth refusing the save
	}
	used := map[string]bool{}
	for _, e := range out {
		if !e.IsDeleted && e.FileID != "" {
			used[e.FileID] = true
		}
	}
	files := map[string]json.RawMessage{}
	for id, f := range inFiles {
		files[id] = f
	}
	for id, f := range curFiles {
		if _, ok := files[id]; !ok && used[id] {
			files[id] = f
			changed = true
		}
	}
	if !changed {
		return incoming, false, nil
	}
	for id := range files {
		if !used[id] {
			delete(files, id)
		}
	}

	raws := make([]json.RawMessage, len(out))
	for i, e := range out {
		raws[i] = e.raw
	}
	if in["elements"], err = json.Marshal(raws); err != nil {
		return nil, false, err
	}
	if in["files"], err = json.Marshal(files); err != nil {
		return nil, false, err
	}
	merged, err = json.MarshalIndent(in, "", "  ")
	return merged, true, err
}

// newer reports whether a beats b under Excalidraw's rule.
func newer(a, b stamp) bool {
	if a.Version != b.Version {
		return a.Version > b.Version
	}
	return a.VersionNonce < b.VersionNonce
}

// ordered reports whether every element carries a fractional index, so the
// merged list can be put in Excalidraw's z-order. Older scenes have none and
// keep the incoming order with the disk's extras after it.
func ordered(els []element) bool {
	for _, e := range els {
		if e.Index == "" {
			return false
		}
	}
	return true
}

func elements(raw json.RawMessage) ([]element, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("drawing: elements is not a list: %w", err)
	}
	out := make([]element, 0, len(list))
	for _, r := range list {
		var s stamp
		if err := json.Unmarshal(r, &s); err != nil {
			return nil, fmt.Errorf("drawing: bad element: %w", err)
		}
		out = append(out, element{stamp: s, raw: r})
	}
	return out, nil
}
