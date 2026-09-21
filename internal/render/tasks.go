// Task extraction: the same lines the rendered checkbox comes from,
// found line by line so a scan of a large tree never parses markdown
// twice. A task is a list item (-, * or +, or a number) whose text
// starts with a GFM checkbox; code fences and blockquotes hold text that
// is quoted or shown verbatim, not work the note's author means to do.
package render

import (
	"regexp"
	"strings"
)

// Task is one `- [ ]` line of a note body.
type Task struct {
	// Line is the 0-based line of the marker, counted from the start of
	// the body. It matches the data-line the rendered checkbox carries.
	Line int
	// Indent is the list nesting depth in levels of two spaces.
	Indent int
	// HTML is the task's text after the marker, rendered to inline HTML
	// with the same engine (and extensions) the note render uses, so
	// bold, wikilinks and tags come out the same as they do in the note.
	HTML string
	// Done reports the box's state.
	Done bool
	// Heading is the nearest heading above the task, '' at note top
	// level. Inside a code fence a line that looks like a heading is
	// text, so it does not count.
	Heading string
}

var (
	taskLine    = regexp.MustCompile(`^(\s*)(?:[-*+]|\d+[.)])\s+\[([ xX])][ \t]?(.*)$`)
	headingLine = regexp.MustCompile(`^#{1,6}[ \t]+(.+?)[ \t#]*$`)
	fenceLine   = regexp.MustCompile(`^(\s*)(` + "```" + `|~~~)`)
)

// Tasks returns the tasks of a note body in file order.
func Tasks(body []byte) []Task {
	var out []Task
	heading := ""
	inFence := false
	for i, raw := range strings.Split(string(body), "\n") {
		if m := fenceLine.FindStringSubmatch(raw); m != nil {
			if m[1] == "" {
				inFence = !inFence
			}
			continue
		}
		if inFence {
			continue
		}
		trimmed := strings.TrimLeft(raw, " \t")
		if strings.HasPrefix(trimmed, ">") {
			// A quoted list is a quotation, not the author's task.
			continue
		}
		if m := headingLine.FindStringSubmatch(raw); m != nil {
			heading = strings.TrimSpace(m[1])
			continue
		}
		m := taskLine.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		t := Task{
			Line:    i,
			Indent:  len(m[1]) / 2,
			HTML:    InlineHTML(m[3]),
			Done:    m[2] != " ",
			Heading: heading,
		}
		if t.Indent > 8 {
			t.Indent = 8
		}
		out = append(out, t)
	}
	return out
}

// InlineHTML renders one line of markdown as the inner HTML of a
// paragraph: the same engine the note render uses, with the wrapping
// <p> stripped. A line that renders as another block kind keeps it; the
// task row's styles flatten whatever comes back.
func InlineHTML(line string) string {
	line = strings.TrimRight(line, " \t")
	if line == "" {
		return ""
	}
	html, err := Markdown([]byte(line))
	if err != nil || len(html) == 0 {
		return ""
	}
	s := string(html)
	if strings.HasPrefix(s, "<p>") {
		if i := strings.LastIndex(s, "</p>\n"); i >= 0 {
			s = s[len("<p>"):i]
		}
	}
	return s
}
