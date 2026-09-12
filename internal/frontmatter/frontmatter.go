// Package frontmatter reads and minimally edits the YAML block at the top of
// a note. The contract (docs/file-format.md) is deliberately small: id and
// created are the only required keys, and the writer never reorders,
// reformats, or removes anything the user put there.
//
// The parser is intentionally not a full YAML implementation. Notes are
// hand-edited files; treating the block as lines of `key: value` keeps the
// round trip byte-exact, which a YAML re-serialisation would not.
package frontmatter

import (
	"bytes"
	"strconv"
	"strings"
	"time"
)

// Meta holds the keys the application understands.
type Meta struct {
	ID       string
	Created  time.Time
	Order    *int
	Trusted  bool
	Template string
	// Raw is every key/value pair in the block, in file order, including
	// ones the application does not interpret.
	Raw [][2]string
}

// Doc is a parsed note: the frontmatter block boundaries plus the body.
type Doc struct {
	Meta Meta
	// HasBlock reports whether the file started with a frontmatter block.
	HasBlock bool
	// Body is the content after the block (or the whole file if none).
	Body []byte
	// blockEnd is the byte offset just past the closing delimiter line.
	blockEnd int
	newline  string
}

var delim = []byte("---")

// Parse splits content into frontmatter and body. A block is recognised
// only when the file begins with "---" on its own line and a matching
// "---" (or "...") line follows.
func Parse(content []byte) Doc {
	d := Doc{Body: content, newline: "\n"}
	if bytes.Contains(content, []byte("\r\n")) {
		d.newline = "\r\n"
	}
	first, rest, ok := cutLine(content)
	if !ok || !bytes.Equal(bytes.TrimRight(first, "\r"), delim) {
		return d
	}
	offset := len(content) - len(rest)
	var lines [][]byte
	for {
		line, next, more := cutLine(rest)
		trimmed := bytes.TrimRight(line, "\r")
		consumed := len(rest) - len(next)
		if bytes.Equal(trimmed, delim) || bytes.Equal(trimmed, []byte("...")) {
			d.HasBlock = true
			d.blockEnd = offset + consumed
			d.Body = content[d.blockEnd:]
			break
		}
		lines = append(lines, trimmed)
		offset += consumed
		rest = next
		if !more {
			// Unterminated block: treat the whole file as body.
			return Doc{Body: content, newline: d.newline}
		}
	}
	for _, line := range lines {
		k, v, ok := splitKV(string(line))
		if !ok {
			continue
		}
		d.Meta.Raw = append(d.Meta.Raw, [2]string{k, v})
		switch k {
		case "id":
			d.Meta.ID = v
		case "created":
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				d.Meta.Created = t
			} else if t, err := time.Parse("2006-01-02", v); err == nil {
				d.Meta.Created = t
			}
		case "order":
			if n, err := strconv.Atoi(v); err == nil {
				d.Meta.Order = &n
			}
		case "trusted":
			d.Meta.Trusted = v == "true" || v == "yes"
		case "template":
			d.Meta.Template = v
		}
	}
	return d
}

// cutLine returns the first line without its "\n", the remainder, and
// whether a newline was present.
func cutLine(b []byte) ([]byte, []byte, bool) {
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		return b, nil, false
	}
	return b[:i], b[i+1:], true
}

func splitKV(line string) (string, string, bool) {
	if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
		return "", "", false
	}
	i := strings.Index(line, ":")
	if i <= 0 {
		return "", "", false
	}
	k := strings.TrimSpace(line[:i])
	v := strings.TrimSpace(line[i+1:])
	v = strings.Trim(v, "\"'")
	if strings.ContainsAny(k, " \t") {
		return "", "", false
	}
	return k, v, true
}

// EnsureID returns content with an id (and created, when absent) written
// into the frontmatter. Existing keys and their formatting are untouched:
// the new lines are inserted at the top of an existing block, or a new
// block is prepended when there is none. changed reports whether any bytes
// differ from the input.
func EnsureID(content []byte, id string, now time.Time) (out []byte, meta Meta, changed bool) {
	d := Parse(content)
	if d.Meta.ID != "" {
		return content, d.Meta, false
	}
	nl := d.newline
	var insert strings.Builder
	insert.WriteString("id: " + id + nl)
	if d.Meta.Created.IsZero() {
		// Second precision on disk, so keep the in-memory value identical to
		// what a later parse of the file will produce.
		created := now.UTC().Truncate(time.Second)
		insert.WriteString("created: " + created.Format(time.RFC3339) + nl)
		d.Meta.Created = created
	}
	d.Meta.ID = id

	if d.HasBlock {
		// Insert after the opening delimiter line.
		_, rest, _ := cutLine(content)
		head := content[:len(content)-len(rest)]
		out = make([]byte, 0, len(content)+insert.Len())
		out = append(out, head...)
		out = append(out, insert.String()...)
		out = append(out, rest...)
		return out, d.Meta, true
	}
	out = make([]byte, 0, len(content)+insert.Len()+8)
	out = append(out, "---"+nl...)
	out = append(out, insert.String()...)
	out = append(out, "---"+nl...)
	out = append(out, content...)
	return out, d.Meta, true
}

// ReplaceID rewrites the value of an existing id line in place, keeping
// every other byte. It returns the input unchanged when there is no block
// or no id key (use EnsureID for that case).
func ReplaceID(content []byte, id string) ([]byte, bool) {
	d := Parse(content)
	if !d.HasBlock || d.Meta.ID == "" {
		return content, false
	}
	_, rest, _ := cutLine(content)
	head := len(content) - len(rest)
	offset := head
	for {
		line, next, more := cutLine(rest)
		trimmed := bytes.TrimRight(line, "\r")
		if bytes.Equal(trimmed, delim) || bytes.Equal(trimmed, []byte("...")) {
			return content, false
		}
		if k, _, ok := splitKV(string(trimmed)); ok && k == "id" {
			out := make([]byte, 0, len(content)+len(id))
			out = append(out, content[:offset]...)
			out = append(out, "id: "+id...)
			out = append(out, line[len(trimmed):]...) // keep a trailing \r
			out = append(out, '\n')
			out = append(out, next...)
			return out, true
		}
		offset += len(rest) - len(next)
		rest = next
		if !more {
			return content, false
		}
	}
}
