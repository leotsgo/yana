// Package drawing is the file format of Excalidraw drawings. A drawing is a
// markdown file named *.excalidraw.md whose body holds the scene as JSON in
// an ```excalidraw fence, so it syncs, versions and indexes like any note.
package drawing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

const (
	// Ext is the file suffix that marks a note as a drawing.
	Ext = ".excalidraw.md"
	// Kind is the note kind of a drawing.
	Kind = "drawing"
)

// EmptyScene is the scene of a new drawing.
const EmptyScene = `{"type":"excalidraw","version":2,"source":"yana","elements":[],"appState":{},"files":{}}`

// Element is the part of an Excalidraw element yana reads. Link is a pointer
// because Excalidraw writes "link": null.
type Element struct {
	Type         string  `json:"type"`
	Text         string  `json:"text"`
	OriginalText string  `json:"originalText"`
	Link         *string `json:"link"`
	IsDeleted    bool    `json:"isDeleted"`
}

// Scene is the part of an Excalidraw scene yana reads.
type Scene struct {
	Type     string    `json:"type"`
	Elements []Element `json:"elements"`
}

// ErrNoScene means the body has no excalidraw fence.
var ErrNoScene = errors.New("drawing: no excalidraw block")

// Is reports whether rel names a drawing, ignoring case.
func Is(rel string) bool {
	return strings.HasSuffix(strings.ToLower(rel), Ext)
}

// SplitExt splits name into stem and extension. The extension of a drawing is
// the whole .excalidraw.md suffix, as written; otherwise it is path.Ext.
func SplitExt(name string) (stem, ext string) {
	if Is(name) {
		i := len(name) - len(Ext)
		return name[:i], name[i:]
	}
	ext = path.Ext(name)
	return strings.TrimSuffix(name, ext), ext
}

// Block returns the JSON inside the first ```excalidraw fence of body, which
// has its frontmatter already stripped. It returns ErrNoScene if there is none.
func Block(body []byte) ([]byte, error) {
	var out [][]byte
	open := false
	for _, line := range bytes.Split(body, []byte("\n")) {
		l := bytes.TrimRight(line, " \t\r")
		switch {
		case !open:
			open = string(l) == "```excalidraw"
		case string(l) == "```":
			return bytes.Join(out, []byte("\n")), nil
		default:
			out = append(out, line)
		}
	}
	return nil, ErrNoScene
}

// Parse returns the scene in body and its raw JSON.
func Parse(body []byte) (Scene, []byte, error) {
	raw, err := Block(body)
	if err != nil {
		return Scene{}, nil, err
	}
	s, err := parseScene(raw)
	return s, raw, err
}

// Validate checks that sceneJSON is an Excalidraw scene.
func Validate(sceneJSON []byte) error {
	_, err := parseScene(sceneJSON)
	return err
}

func parseScene(raw []byte) (Scene, error) {
	var s Scene
	if err := json.Unmarshal(raw, &s); err != nil {
		return Scene{}, fmt.Errorf("drawing: bad scene: %w", err)
	}
	if s.Type != "excalidraw" {
		return Scene{}, fmt.Errorf("drawing: scene type is %q, want excalidraw", s.Type)
	}
	return s, nil
}

// Encode wraps sceneJSON in an excalidraw fence, ready to be a note body.
func Encode(sceneJSON []byte) []byte {
	return []byte("```excalidraw\n" + string(sceneJSON) + "\n```\n")
}

// Text is the text of the live text elements, one per line. It prefers the
// original text, which keeps [[wikilinks]] that wrapping may have split.
func (s Scene) Text() string {
	var parts []string
	for _, e := range s.Elements {
		if e.Type != "text" || e.IsDeleted {
			continue
		}
		t := e.OriginalText
		if t == "" {
			t = e.Text
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, "\n")
}

// Links returns the targets of the live elements whose link is a wikilink,
// in first-seen order without duplicates.
func (s Scene) Links() []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range s.Elements {
		if e.IsDeleted || e.Link == nil {
			continue
		}
		l := strings.TrimSpace(*e.Link)
		if !strings.HasPrefix(l, "[[") || !strings.HasSuffix(l, "]]") {
			continue
		}
		target, _, _ := strings.Cut(l[2:len(l)-2], "|")
		if target = strings.TrimSpace(target); target != "" && !seen[target] {
			seen[target] = true
			out = append(out, target)
		}
	}
	return out
}
