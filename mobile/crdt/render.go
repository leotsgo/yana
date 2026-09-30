package crdt

import (
	"strings"

	"github.com/madeofpendletonwool/yana/internal/render"
)

// RenderMarkdown renders a note body to HTML on the device, through the
// same goldmark engine the server renders with, so the phone's reader
// and the web's reading mode produce the same markup by construction.
// Wikilinks and tags come out as spans carrying their targets and task
// boxes carry data-line; the reader resolves and wires them itself.
func RenderMarkdown(body string) (string, error) {
	out, err := render.Markdown([]byte(body))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// WikiLinks returns the distinct raw targets of the wikilinks in body,
// in first-seen order, joined with newlines — the same extraction the
// renderer performed, so a client resolving links from its own replica
// asks about exactly the targets the render emitted (and none that sit
// inside code). gomobile cannot return a string slice; a wikilink
// target cannot contain a newline, so the join is unambiguous.
func WikiLinks(body string) string {
	return strings.Join(render.WikiLinks([]byte(body)), "\n")
}
