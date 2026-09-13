// Package wikilink resolves [[target]] references between notes of one
// space. Resolution is scoped to the space because a space is the sharing
// boundary: a link never reaches into another top-level directory.
//
// The order is fixed: exact relative path from the linking note, exact path
// from the space root, unique filename match anywhere in the space, then
// unresolved.
package wikilink

import (
	"path"
	"strings"
)

// Rule says which step of the resolution order matched.
type Rule string

const (
	RuleRelative Rule = "relative"
	RuleRoot     Rule = "root"
	RuleFilename Rule = "filename"
)

// NoteRef is one indexed note a target can resolve to.
type NoteRef struct {
	ID      string
	RelPath string // relative to the notes root, space prefix included
}

// Resolver resolves raw targets within one space.
type Resolver struct {
	space  string
	byPath map[string]string   // rel path → note id
	byBase map[string][]string // base name → note ids
}

// NewResolver indexes the notes of one space. space is the top-level
// directory name, "" for notes loose in the root.
func NewResolver(space string, refs []NoteRef) *Resolver {
	r := &Resolver{
		space:  space,
		byPath: make(map[string]string, len(refs)),
		byBase: make(map[string][]string, len(refs)),
	}
	for _, ref := range refs {
		r.byPath[ref.RelPath] = ref.ID
		base := path.Base(ref.RelPath)
		r.byBase[base] = append(r.byBase[base], ref.ID)
	}
	return r
}

// Resolution is the outcome for one raw target.
type Resolution struct {
	ToID string
	Rule Rule
	OK   bool
}

// Resolve follows the resolution order for one raw target written in the
// note at fromRel (relative to the notes root, space prefix included).
func (r *Resolver) Resolve(raw, fromRel string) Resolution {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "[]\n\r") {
		return Resolution{}
	}
	// 1. Exact relative path from the linking note's directory.
	if p, ok := r.pathWithin(raw, path.Dir(fromRel)); ok {
		if id, ok := r.byPath[p]; ok {
			return Resolution{ToID: id, Rule: RuleRelative, OK: true}
		}
	}
	// 2. Exact path from the space root.
	if p, ok := r.pathWithin(raw, r.space); ok {
		if id, ok := r.byPath[p]; ok {
			return Resolution{ToID: id, Rule: RuleRoot, OK: true}
		}
	}
	// 3. Unique filename match anywhere in the space. Only a bare filename
	// falls through to this step: a target that spells out a path was a
	// path reference and must not surprise-resolve by basename.
	if !strings.Contains(raw, "/") {
		cands := r.byBase[withExtension(raw)]
		if len(cands) == 1 {
			return Resolution{ToID: cands[0], Rule: RuleFilename, OK: true}
		}
	}
	return Resolution{}
}

// pathWithin joins base (a directory relative to the notes root) and raw,
// infers the .md extension when raw carries none, and reports the cleaned
// path when it stays inside the resolver's space.
func (r *Resolver) pathWithin(raw, base string) (string, bool) {
	p := withExtension(path.Join(base, raw))
	if r.space == "" {
		// Notes loose in the root: their space is the root itself.
		return p, !strings.HasPrefix(p, "../") && p != ".." && !path.IsAbs(p)
	}
	prefix := r.space + "/"
	if !strings.HasPrefix(p, prefix) {
		return "", false
	}
	return p, true
}

// withExtension appends .md when the target carries no note extension. A
// target that already ends in .md, .markdown, .html, or .htm is used as
// written; targets with any other extension are left alone and will not
// resolve (they are not notes).
func withExtension(p string) string {
	ext := strings.ToLower(path.Ext(p))
	switch ext {
	case ".md", ".markdown", ".html", ".htm":
		return p
	}
	return p + ".md"
}

// HasExtension reports whether the raw target names a note extension.
func HasExtension(raw string) bool {
	switch strings.ToLower(path.Ext(strings.TrimSpace(raw))) {
	case ".md", ".markdown", ".html", ".htm":
		return true
	}
	return false
}

// RewriteTarget computes the raw target that keeps a link pointing at a
// note after it moved to newRel, preserving the style the author chose:
// relative links stay relative to the linking note, root-path links stay
// rooted, filename links keep naming just the file. fromRel is the linking
// note's path, newRel the moved note's new path, both relative to the notes
// root with the space prefix included, and both inside the same space.
// keepExtension says whether the original raw target spelled out the
// extension.
func RewriteTarget(rule Rule, fromRel, newRel string, keepExtension bool) string {
	target := newRel
	if !keepExtension {
		target = strings.TrimSuffix(target, path.Ext(newRel))
	}
	switch rule {
	case RuleRelative:
		return relSlash(path.Dir(fromRel), target)
	case RuleFilename:
		return path.Base(target)
	default: // RuleRoot: relative to the space root.
		if i := strings.IndexByte(newRel, '/'); i >= 0 {
			return target[i+1:]
		}
		return target
	}
}

// relSlash expresses target relative to the slash path dir. Both inputs are
// clean paths inside the same space, so the walk cannot fail.
func relSlash(dir, target string) string {
	if dir == "." {
		return target
	}
	bp := strings.Split(dir, "/")
	tp := strings.Split(target, "/")
	i := 0
	for i < len(bp) && i < len(tp) && bp[i] == tp[i] {
		i++
	}
	var out []string
	for j := i; j < len(bp); j++ {
		out = append(out, "..")
	}
	out = append(out, tp[i:]...)
	return path.Join(out...)
}
