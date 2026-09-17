// The static site export: a space or subtree rendered to a directory of
// HTML pages with the assets beside them. Navigation comes from the real
// directory structure, resolved wikilinks become relative hrefs, every
// page carries its backlinks, and a prebuilt search index plus a small
// client-side runtime make search work offline from file:// — no server,
// no fetches. Deployable to any static host.
package export

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/render"
)

// SiteStats reports what a site export wrote.
type SiteStats struct {
	Notes  int // notes rendered as pages
	Assets int // non-note files copied
	Pages  int // every page in the site, home and search included
}

// siteNote is one note placed in the site.
type siteNote struct {
	n        index.Note
	sitePath string // path within the site, e.g. "sub/second.html"
	href     string // escaped sitePath, ready for an href attribute
}

// siteTree is the navigation tree, mirroring the directory structure of
// the exported subtree.
type siteTree struct {
	name     string
	note     *siteNote
	children []*siteTree
}

// SiteZip writes a static site of one space, or a subtree of it, as a zip
// archive. subtree is a path within the space ("" is the whole space).
func (d *Deps) SiteZip(ctx context.Context, space, subtree string, w io.Writer) (SiteStats, error) {
	zw := zip.NewWriter(w)
	stats, err := d.writeSite(ctx, zw, space, subtree)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	return stats, err
}

// writeSite renders the site into an open zip writer.
func (d *Deps) writeSite(ctx context.Context, zw *zip.Writer, space, subtree string) (SiteStats, error) {
	var stats SiteStats
	baseRel, notes, err := d.scope(space, subtree)
	if err != nil {
		return stats, err
	}
	byID := make(map[string]*siteNote, len(notes))
	byPath := make(map[string]*siteNote, len(notes))
	noteFiles := make(map[string]bool, len(notes))
	for i := range notes {
		sn := &notes[i]
		if prev, taken := byPath[sn.sitePath]; taken {
			return stats, fmt.Errorf("%s and %s both export to %s; rename one of them", prev.n.RelPath, sn.n.RelPath, sn.sitePath)
		}
		byPath[sn.sitePath] = sn
		byID[sn.n.ID] = sn
		noteFiles[sn.n.RelPath] = true
	}
	stats.Notes = len(notes)
	tree := buildSiteTree(notes)
	now := d.now()

	// One page per note.
	for i := range notes {
		sn := &notes[i]
		page, err := d.sitePage(ctx, sn, tree, byID, now)
		if err != nil {
			return stats, err
		}
		if err := put(zw, sn.sitePath, now, page); err != nil {
			return stats, err
		}
		stats.Pages++
	}

	// The shared stylesheet and the icon.
	if err := put(zw, "site.css", now, []byte(siteCSS)); err != nil {
		return stats, err
	}
	if len(d.Favicon) > 0 {
		if err := put(zw, "favicon.png", now, d.Favicon); err != nil {
			return stats, err
		}
	}

	// The home page: navigation plus every note as a plain list.
	home := fmt.Sprintf(`<h1>%s</h1>
<p class="x-muted">%d notes, exported %s.</p>
<ul class="x-home-list">%s</ul>`,
		esc(displaySpace(space, subtree)), stats.Notes, now.Format("2006-01-02"), homeList(notes))
	if err := put(zw, "index.html", now, d.siteChrome("index.html", displaySpace(space, subtree), tree, home, now)); err != nil {
		return stats, err
	}
	stats.Pages++

	// Search: the bundled runtime plus a prebuilt index. Both load as
	// classic scripts, which is what makes them work from file://.
	if len(d.SearchJS) > 0 {
		idx, err := d.searchIndex(ctx, space, notes)
		if err != nil {
			return stats, err
		}
		search := fmt.Sprintf(`<h1>Search</h1>
<input id="yana-search" class="x-searchbox" type="search" placeholder="Search these notes" autocomplete="off" spellcheck="false" aria-label="Search these notes">
<div id="yana-results" class="x-results" aria-live="polite"><p class="x-muted">Type to search %d notes.</p></div>
<script src="search.js"></script>
<script src="search-index.js"></script>`, stats.Notes)
		if err := put(zw, "search.html", now, d.siteChrome("search.html", "Search", tree, search, now)); err != nil {
			return stats, err
		}
		if err := put(zw, "search.js", now, d.SearchJS); err != nil {
			return stats, err
		}
		if err := put(zw, "search-index.js", now, idx); err != nil {
			return stats, err
		}
		stats.Pages += 3
	}

	// Everything beside the notes: images in _assets and any other
	// non-note file a note may point at, copied byte for byte. Paths are
	// relative to the site root, so a note's relative references keep
	// resolving beside its page.
	var copyErr error
	walkErr := walkTree(d.Root, baseRel, func(rel string, info os.FileInfo, hidden bool) {
		if hidden || noteFiles[rel] || isSpaceConfig(rel) {
			return // hidden files, notes (they become pages) and the space file are not site content
		}
		abs, _, rerr := d.Root.Resolve(rel)
		if rerr != nil {
			return
		}
		data, rerr := os.ReadFile(abs)
		if rerr != nil {
			return
		}
		stats.Assets++
		if copyErr == nil {
			copyErr = put(zw, strings.TrimPrefix(strings.TrimPrefix(rel, baseRel), "/"), info.ModTime(), data)
		}
	})
	if walkErr != nil {
		return stats, walkErr
	}
	if copyErr != nil {
		return stats, copyErr
	}
	return stats, nil
}

// scope resolves the export scope: a validated subtree path within the
// space, the base path relative to the notes root, and the notes in it.
func (d *Deps) scope(space, subtree string) (string, []siteNote, error) {
	sub, err := d.Root.Clean(subtree)
	if err != nil {
		return "", nil, err
	}
	notes, err := d.DB.ListNotes(context.Background(), space)
	if err != nil {
		return "", nil, err
	}
	prefix := ""
	baseRel := space
	if sub != "" {
		prefix = sub + "/"
		baseRel = space + "/" + sub
	}
	var out []siteNote
	for _, n := range notes {
		inSpace := strings.TrimPrefix(n.RelPath, space+"/")
		if prefix != "" && !strings.HasPrefix(inSpace, prefix) {
			continue
		}
		// The site root is the subtree: pages and assets are placed
		// relative to it, so a note's relative references keep working.
		out = append(out, siteNote{n: n, sitePath: pagePath(strings.TrimPrefix(inSpace, prefix))})
	}
	if len(out) == 0 {
		return "", nil, ErrNoNotes
	}
	return baseRel, out, nil
}

// sitePage renders one note into a full page with navigation and
// backlinks.
func (d *Deps) sitePage(ctx context.Context, sn *siteNote, tree *siteTree, byID map[string]*siteNote, now time.Time) ([]byte, error) {
	doc, err := d.readNote(sn.n)
	if err != nil {
		return nil, err
	}
	links, err := d.DB.OutboundLinks(ctx, sn.n.ID)
	if err != nil {
		return nil, err
	}
	var body []byte
	switch sn.n.Kind {
	case "md":
		body, err = render.Markdown(doc.Body)
		if err != nil {
			return nil, err
		}
		body = rewriteWikiSpans(body, links, sn, byID)
	case "html":
		body = doc.Body
		if !doc.Meta.Trusted {
			body = render.SanitizeHTML(body)
		}
		body = rewriteWikiAnchors(body, links, sn, byID)
	default:
		return nil, fmt.Errorf("note %s has unknown kind %q", sn.n.ID, sn.n.Kind)
	}
	// Backlinks, limited to notes the site actually carries.
	back, err := d.DB.Backlinks(ctx, sn.n.ID)
	if err == nil && len(back) > 0 {
		var rows strings.Builder
		for _, b := range back {
			target, ok := byID[b.Note.ID]
			if !ok {
				continue
			}
			rows.WriteString(`<li><a href="` + hrefBetween(sn.sitePath, target.sitePath) + `">` + esc(titleOf(target)) + `</a>`)
			if b.Context != "" {
				rows.WriteString(`<p class="x-context">` + esc(b.Context) + `</p>`)
			}
			rows.WriteString(`</li>`)
		}
		if rows.Len() > 0 {
			body = append(body, []byte(`<section class="x-backlinks"><h2>Linked from</h2><ul>`+rows.String()+`</ul></section>`)...)
		}
	}
	inSpace := strings.TrimPrefix(sn.n.RelPath, sn.n.Space+"/")
	content := `<header class="x-path">` + esc(inSpace) + `</header>` + string(body)
	return d.siteChrome(sn.sitePath, titleOf(sn), tree, content, now), nil
}

// markImg is the mark beside the wordmark, or nothing when there is no
// icon to show.
func markImg(src string) string {
	if src == "" {
		return ""
	}
	return `<img class="mark" src="` + src + `" alt="" width="18" height="18">`
}

// siteChrome wraps content in the site document: the title, stylesheet,
// navigation tree, and search link.
func (d *Deps) siteChrome(pagePath, title string, tree *siteTree, content string, now time.Time) []byte {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<title>" + esc(title) + "</title>\n")
	b.WriteString("<link rel=\"stylesheet\" href=\"" + hrefBetween(pagePath, "site.css") + "\">\n")
	icon := ""
	if len(d.Favicon) > 0 {
		icon = hrefBetween(pagePath, "favicon.png")
		b.WriteString("<link rel=\"icon\" type=\"image/png\" href=\"" + icon + "\">\n")
	}
	b.WriteString("</head>\n<body>\n<div class=\"x-layout\">\n<nav class=\"x-nav\">\n")
	b.WriteString(`<div class="x-nav-head"><a class="wordmark" href="` + hrefBetween(pagePath, "index.html") + `">` + markImg(icon) + `YANA/</a>`)
	if len(d.SearchJS) > 0 {
		b.WriteString(`<a class="x-search-link" href="` + hrefBetween(pagePath, "search.html") + `">Search</a>`)
	}
	b.WriteString("</div>\n")
	b.WriteString(`<div class="x-tree">` + renderTree(tree, pagePath) + `</div>`)
	b.WriteString("\n</nav>\n<main class=\"x-main x-note\">\n")
	b.WriteString(content)
	b.WriteString("\n</main>\n</div>\n</body>\n</html>\n")
	return []byte(b.String())
}

// renderTree writes the navigation tree as nested HTML. Links are
// relative to the page being rendered; the page itself is marked.
func renderTree(t *siteTree, pagePath string) string {
	var b strings.Builder
	for _, c := range t.children {
		if c.note != nil {
			current := ""
			if c.note.sitePath == pagePath {
				current = " current"
			}
			b.WriteString(`<a class="x-link` + current + `" href="` + hrefBetween(pagePath, c.note.sitePath) + `" title="` + esc(c.note.n.RelPath) + `">` + esc(titleOf(c.note)) + `</a>`)
			continue
		}
		b.WriteString(`<div class="x-dir"><span class="x-dirname">` + esc(c.name) + `/</span>`)
		if inner := renderTree(c, pagePath); inner != "" {
			b.WriteString(inner)
		}
		b.WriteString(`</div>`)
	}
	return b.String()
}

// buildSiteTree nests the flat note list into directories the way the
// app's sidebar does: directories first, then notes by explicit order
// and title.
func buildSiteTree(notes []siteNote) *siteTree {
	root := &siteTree{}
	for i := range notes {
		sn := &notes[i]
		cur := root
		parts := strings.Split(sn.sitePath, "/")
		for _, part := range parts[:len(parts)-1] {
			cur = childTree(cur, part)
		}
		cur.children = append(cur.children, &siteTree{name: parts[len(parts)-1], note: sn})
	}
	sortTree(root)
	return root
}

func childTree(parent *siteTree, name string) *siteTree {
	for _, c := range parent.children {
		if c.note == nil && c.name == name {
			return c
		}
	}
	c := &siteTree{name: name}
	parent.children = append(parent.children, c)
	return c
}

func sortTree(t *siteTree) {
	sort.SliceStable(t.children, func(i, j int) bool {
		a, b := t.children[i], t.children[j]
		if (a.note == nil) != (b.note == nil) {
			return a.note == nil // directories first
		}
		if a.note != nil {
			switch {
			case a.note.n.Order != nil && b.note.n.Order != nil && *a.note.n.Order != *b.note.n.Order:
				return *a.note.n.Order < *b.note.n.Order
			case a.note.n.Order != nil && b.note.n.Order == nil:
				return true
			case a.note.n.Order == nil && b.note.n.Order != nil:
				return false
			}
			return strings.ToLower(titleOf(a.note)) < strings.ToLower(titleOf(b.note))
		}
		return strings.ToLower(a.name) < strings.ToLower(b.name)
	})
	for _, c := range t.children {
		if c.note == nil {
			sortTree(c)
		}
	}
}

// homeList writes the flat list of notes for the home page.
func homeList(notes []siteNote) string {
	var b strings.Builder
	for i := range notes {
		sn := &notes[i]
		b.WriteString(`<li><a href="` + sn.href + `">` + esc(titleOf(sn)) + `</a><span class="x-note-path">` + esc(sn.n.RelPath) + `</span></li>`)
	}
	return b.String()
}

// titleOf is the note's title, falling back to its file name.
func titleOf(sn *siteNote) string {
	if sn.n.Title != "" {
		return sn.n.Title
	}
	return path.Base(sn.n.RelPath)
}

// displaySpace names the site after the space or subtree it holds.
func displaySpace(space, subtree string) string {
	if subtree != "" {
		return space + "/" + subtree
	}
	return space
}

// wikiSpanRe matches the wikilink spans the markdown renderer emits:
// <span class="wikilink" data-target="…">display</span>. The display
// text is already HTML-escaped by the renderer.
var wikiSpanRe = regexp.MustCompile(`(?s)<span class="wikilink" data-target="([^"]*)">(.*?)</span>`)

// rewriteWikiSpans turns rendered wikilink spans into links between the
// site's pages. Unresolved targets stay plain text, styled as unresolved.
func rewriteWikiSpans(body []byte, links []index.OutboundLink, sn *siteNote, byID map[string]*siteNote) []byte {
	targets := resolvedTargets(links)
	return wikiSpanRe.ReplaceAllFunc(body, func(m []byte) []byte {
		g := wikiSpanRe.FindSubmatch(m)
		raw := unescapeAttr(string(g[1]))
		display := g[2]
		if id, ok := targets[raw]; ok {
			if target, ok := byID[id]; ok {
				return []byte(`<a class="wikilink" href="` + hrefBetween(sn.sitePath, target.sitePath) + `" title="` + esc(raw) + `">` + string(display) + `</a>`)
			}
		}
		return []byte(`<span class="wikilink unresolved" title="unresolved link">` + string(display) + `</span>`)
	})
}

var (
	anchorTagRe      = regexp.MustCompile(`(?s)<a\b[^>]*>`)
	dataWikilinkRe   = regexp.MustCompile(`\bdata-wikilink="([^"]*)"`)
	wikilinkIDAttrRe = regexp.MustCompile(`(?s)\s+data-wikilink-id="[^"]*"`)
)

// rewriteWikiAnchors gives HTML notes' data-wikilink anchors real hrefs
// between the site's pages. Unresolved anchors keep their text.
func rewriteWikiAnchors(body []byte, links []index.OutboundLink, sn *siteNote, byID map[string]*siteNote) []byte {
	targets := resolvedTargets(links)
	body = wikilinkIDAttrRe.ReplaceAll(body, nil)
	return anchorTagRe.ReplaceAllFunc(body, func(tag []byte) []byte {
		g := dataWikilinkRe.FindSubmatch(tag)
		if g == nil {
			return tag
		}
		raw := unescapeAttr(string(g[1]))
		id, ok := targets[raw]
		if !ok {
			return tag
		}
		target, ok := byID[id]
		if !ok {
			return tag
		}
		// <a + href="…" + the rest of the original tag.
		href := []byte(`<a href="` + hrefBetween(sn.sitePath, target.sitePath) + `"`)
		out := make([]byte, 0, len(href)+len(tag)-2)
		out = append(out, href...)
		out = append(out, tag[2:]...)
		return out
	})
}

// resolvedTargets maps raw wikilink targets to note ids for the resolved
// links of one note.
func resolvedTargets(links []index.OutboundLink) map[string]string {
	var out map[string]string
	for _, l := range links {
		if !l.Resolved || l.ToID == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(links))
		}
		out[l.RawTarget] = l.ToID
	}
	return out
}

func unescapeAttr(s string) string {
	s = strings.ReplaceAll(s, "&#34;", `"`)
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&amp;", "&")
	return s
}

// searchDoc is one note in the prebuilt search index.
type searchDoc struct {
	ID    string `json:"id"`
	Title string `json:"t"`
	Path  string `json:"p"`
	Href  string `json:"h"`
	Body  string `json:"b"`
}

// maxSearchBody bounds the indexed text of one note so a pathological
// note cannot bloat the search payload.
const maxSearchBody = 32 << 10

// searchIndex builds the search-index.js file: the notes of the site as
// JSON on a classic script, which loads from file:// where fetch does
// not.
func (d *Deps) searchIndex(ctx context.Context, space string, notes []siteNote) ([]byte, error) {
	docs := make([]searchDoc, 0, len(notes))
	for i := range notes {
		sn := &notes[i]
		body := d.body(ctx, sn.n.ID)
		if len(body) > maxSearchBody {
			body = body[:maxSearchBody]
		}
		docs = append(docs, searchDoc{ID: sn.n.ID, Title: titleOf(sn), Path: sn.n.RelPath, Href: sn.href, Body: body})
	}
	raw, err := json.Marshal(map[string]any{"space": space, "notes": docs})
	if err != nil {
		return nil, err
	}
	// A literal </ sequence inside a script element would end the tag;
	// escaped, the JSON parses back byte for byte.
	safe := strings.ReplaceAll(string(raw), "</", `<\/`)
	return []byte("window.YANA_EXPORT_INDEX=" + safe + ";\n"), nil
}

// put writes one file into the zip.
func put(zw *zip.Writer, name string, mod time.Time, data []byte) error {
	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: mod}
	hdr.SetMode(0o644)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// isSpaceConfig reports whether rel is a space's .space.yml.
func isSpaceConfig(rel string) bool {
	return path.Base(rel) == ".space.yml"
}
