// Sanitizing HTML notes. Everything a note's body can do to the reader
// passes through here first unless the note's frontmatter says trusted:
// true; the sandboxed frame and the content-origin CSP behind it are the
// second, independent layer.
package render

import (
	"html"
	"regexp"
	"sync"

	"github.com/microcosm-cc/bluemonday"
)

var (
	sanitizeOnce sync.Once
	sanitizeP    *bluemonday.Policy
)

// wikilinkTarget matches the value of a data-wikilink attribute: anything
// printable except the characters that would break out of the attribute.
var wikilinkTarget = regexp.MustCompile(`^[^"<>\x00-\x1f]{1,512}$`)

// sanitizePolicy builds the bluemonday policy for untrusted HTML notes.
// It keeps the document-making parts (structure, tables, lists, images,
// CSS) and drops everything that executes, navigates, or reaches the
// network: script and event handlers are not in the element set, forms are
// not in it, and no URL scheme is whitelisted so absolute URLs (https://,
// data:, javascript:, …) are stripped from src and href values — only
// relative references survive, which the content origin resolves to assets
// in the same space.
func sanitizePolicy() *bluemonday.Policy {
	sanitizeOnce.Do(func() {
		p := bluemonday.NewPolicy()
		// AllowUnsafe only un-gates bluemonday's blanket drop of style
		// elements and their text so a policy may opt into CSS; script
		// stays out of the element set below and is dropped as before.
		p.AllowUnsafe(true)
		p.AllowStandardAttributes()
		p.AllowRelativeURLs(true) // and no schemes: relative references only

		p.AllowElements(
			// structure
			"article", "aside", "section", "nav", "header", "footer", "main",
			"figure", "figcaption", "details", "summary", "hgroup",
			"h1", "h2", "h3", "h4", "h5", "h6",
			"blockquote", "br", "div", "hr", "p", "span", "wbr",
			// phrasing
			"abbr", "acronym", "cite", "code", "dfn", "em", "kbd", "mark",
			"s", "samp", "strong", "sub", "sup", "var", "q", "time",
			"b", "i", "pre", "small", "strike", "tt", "u",
			"bdi", "bdo", "rp", "rt", "ruby", "del", "ins",
			// lists, tables
			"dl", "dt", "dd",
			// media (inert without script; canvas draws nothing sanitized,
			// it is kept so trusted flips do not change the DOM shape)
			"canvas", "picture", "img", "source",
			// CSS as a style element or attribute: CSP on the content
			// origin blocks every load CSS could make (url(), @import),
			// so styling is safe to keep and dashboards need it.
			"style",
		)
		p.AllowLists()
		p.AllowTables()

		p.AllowAttrs("open").Matching(regexp.MustCompile(`(?i)^(|open)$`)).OnElements("details")
		p.AllowAttrs("cite").OnElements("blockquote", "q", "del", "ins")
		p.AllowAttrs("datetime").OnElements("time", "del", "ins")
		p.AllowAttrs("href", "rel", "data-wikilink", "data-wikilink-id").Matching(wikilinkTarget).OnElements("a")
		p.AllowAttrs("src", "alt", "width", "height", "loading").OnElements("img")
		p.AllowAttrs("src", "type", "width", "height").OnElements("source")
		p.AllowAttrs("width", "height").OnElements("canvas")
		// Style attribute values are not CSS-parsed here; the content
		// origin's CSP blocks every load CSS could make (url(),
		// @import), which is the part that matters.
		p.AllowAttrs("style").Globally()

		sanitizeP = p
	})
	return sanitizeP
}

// SanitizeHTML strips scripts, event handlers, forms, and absolute URLs
// from an HTML note body, leaving structure, styling, relative images, and
// data-wikilink anchors intact.
func SanitizeHTML(body []byte) []byte {
	return sanitizePolicy().SanitizeBytes(body)
}

var htmlWikilinkRe = regexp.MustCompile(`(?is)<a\b[^>]*\bdata-wikilink\s*=\s*"([^"]*)"[^>]*>`)

// HTMLWikiLinks returns the distinct data-wikilink targets of an HTML note
// body, in first-seen order. It is the HTML counterpart of WikiLinks: the
// index resolves these the same way it resolves [[targets]].
func HTMLWikiLinks(body []byte) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, m := range htmlWikilinkRe.FindAllSubmatch(body, -1) {
		t := html.UnescapeString(string(m[1]))
		if t == "" {
			continue
		}
		if _, dup := seen[t]; !dup {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}
