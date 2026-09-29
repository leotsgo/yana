// Package templates substitutes the {{...}} variables a template note
// may carry. The daily note and "new from template" both run through
// Apply; the variable set is the one documented in docs/editor.md.
//
// Substitution skips fenced code blocks (``` or ~~~), so a template can
// show its own variables in an example. Unknown variables and prompts
// without an answer are left as written.
package templates

import (
	"regexp"
	"strings"
	"time"
)

// Values are what the variables are filled with.
type Values struct {
	// Title is the new note's title. Empty leaves {{title}} as written.
	Title string
	// When is the moment of creation: {{date}} and {{time}} come from it.
	When time.Time
	// User is the creating account's name; empty without accounts.
	User string
	// Space is the space the new note lands in.
	Space string
	// Folder is the folder the new note lands in, relative to the space.
	Folder string
	// Answers holds the {{prompt:Label}} answers, by label.
	Answers map[string]string
}

// Result is what Apply made of a template body.
type Result struct {
	// Body is the substituted text.
	Body string
	// Cursor is where {{cursor}} was, counted in runes from the start of
	// Body; -1 when the template carries no marker. Every occurrence is
	// removed; the first says where the caret lands.
	Cursor int
	// Prompts are the distinct {{prompt:Label}} labels in the body, in
	// first-occurrence order, whether or not they were answered.
	Prompts []string
}

var varRe = regexp.MustCompile(`\{\{([^{}\r\n]+)\}\}`)

// fenceRe matches the opening or closing line of a fenced code block:
// three or more backticks or tildes, at most three spaces in.
var fenceRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")

// Apply substitutes the variables in a template body.
func Apply(body string, v Values) Result {
	r := Result{Cursor: -1}
	seen := map[string]bool{}
	var out strings.Builder
	// written counts runes out so far, which is what Cursor reports.
	written := 0
	fenced := false
	for _, line := range splitLines(body) {
		if fenced {
			out.WriteString(line)
			written += len([]rune(line))
			if fenceRe.MatchString(line) {
				fenced = false
			}
			continue
		}
		if fenceRe.MatchString(line) {
			fenced = true
			out.WriteString(line)
			written += len([]rune(line))
			continue
		}
		written += substituteLine(&out, line, v, &r, seen, written)
	}
	r.Body = out.String()
	return r
}

// substituteLine writes one line to out with its variables substituted,
// returning how many runes it wrote.
func substituteLine(out *strings.Builder, line string, v Values, r *Result, seen map[string]bool, at int) int {
	matches := varRe.FindAllStringSubmatchIndex(line, -1)
	if matches == nil {
		out.WriteString(line)
		return len([]rune(line))
	}
	before := 0
	count := 0
	for _, m := range matches {
		chunk := line[before:m[0]]
		out.WriteString(chunk)
		count += len([]rune(chunk))
		name := line[m[2]:m[3]]
		repl, isCursor := variable(name, v, r, seen)
		if isCursor && r.Cursor < 0 {
			r.Cursor = at + count
		}
		if !isCursor {
			out.WriteString(repl)
			count += len([]rune(repl))
		}
		before = m[1]
	}
	chunk := line[before:]
	out.WriteString(chunk)
	count += len([]rune(chunk))
	return count
}

// variable resolves one variable name to its replacement. isCursor says
// the marker is {{cursor}}: it writes nothing and only places the caret.
func variable(name string, v Values, r *Result, seen map[string]bool) (repl string, isCursor bool) {
	switch {
	case name == "title":
		if v.Title == "" {
			return "{{title}}", false
		}
		return v.Title, false
	case name == "date":
		return v.When.Format("2006-01-02"), false
	case strings.HasPrefix(name, "date:"):
		return formatDate(strings.TrimSpace(name[5:]), v.When), false
	case name == "time":
		return v.When.Format("15:04"), false
	case name == "user":
		return v.User, false
	case name == "space":
		return v.Space, false
	case name == "folder":
		return v.Folder, false
	case name == "cursor":
		return "", true
	case strings.HasPrefix(name, "prompt:"):
		label := strings.TrimSpace(name[7:])
		if label != "" && !seen[label] {
			seen[label] = true
			r.Prompts = append(r.Prompts, label)
		}
		if ans, ok := v.Answers[label]; ok {
			return ans, false
		}
		return "{{" + name + "}}", false
	default:
		return "{{" + name + "}}", false
	}
}

// dateTokenRe finds a run of letters, so MMMM reads as one unknown
// token rather than two MM.
var dateTokenRe = regexp.MustCompile(`[A-Za-z]+`)

// formatDate expands a {{date:...}} pattern: YYYY, YY, MM and DD are the
// date, HH and mm the clock. Anything else in the pattern stays as it is.
func formatDate(pattern string, when time.Time) string {
	tokens := map[string]string{
		"YYYY": when.Format("2006"),
		"YY":   when.Format("06"),
		"MM":   when.Format("01"),
		"DD":   when.Format("02"),
		"HH":   when.Format("15"),
		"mm":   when.Format("04"),
	}
	var b strings.Builder
	at := 0
	for _, m := range dateTokenRe.FindAllStringIndex(pattern, -1) {
		run := pattern[m[0]:m[1]]
		b.WriteString(pattern[at:m[0]])
		if repl, ok := tokens[run]; ok {
			b.WriteString(repl)
		} else {
			b.WriteString(run)
		}
		at = m[1]
	}
	b.WriteString(pattern[at:])
	return b.String()
}

// splitLines splits s into lines, keeping every "\n" with its line so
// the pieces join back to the original exactly (including a final line
// without one).
func splitLines(s string) []string {
	var lines []string
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, s)
			break
		}
		lines = append(lines, s[:i+1])
		s = s[i+1:]
	}
	return lines
}
