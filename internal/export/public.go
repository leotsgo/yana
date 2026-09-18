// The public page: one note as the content origin serves it to someone
// with a link and no account. It is the single-file export's rendering
// with different plumbing — the pictures beside the note are fetched
// through the link's token rather than inlined, the diagram and math
// runtimes are linked rather than embedded, and a wikilink is a link
// only when its target is public too. Nothing on the page says where
// the note lives: no path, no space, no id.
package export

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/render"
)

// PublicRefs is how the page reaches what sits beside and behind the
// note. Each returns "" to leave a reference alone.
type PublicRefs struct {
	// Asset maps a relative reference from the note's own directory
	// (an image, an attachment) to the URL that serves it.
	Asset func(rel string) string
	// Note maps another note's id to its public URL, when it has one.
	Note func(id string) string
	// Rich maps a runtime file (mermaid.js, katex.js, katex.css) to the
	// URL that serves it.
	Rich func(name string) string
}

// PublicPage renders one note for a public link. HTML notes are always
// sanitized: trusted is a statement about the household, not about
// whoever holds the link.
func (d *Deps) PublicPage(ctx context.Context, n index.Note, refs PublicRefs) ([]byte, error) {
	doc, err := d.readNote(n)
	if err != nil {
		return nil, err
	}
	links, err := d.DB.OutboundLinks(ctx, n.ID)
	if err != nil {
		return nil, err
	}
	targets := resolvedTargets(links)
	noteURL := func(raw string) string {
		id, ok := targets[raw]
		if !ok || refs.Note == nil {
			return ""
		}
		return refs.Note(id)
	}
	var body []byte
	switch n.Kind {
	case "md":
		body, err = render.Markdown(doc.Body)
		if err != nil {
			return nil, err
		}
		body = wikiSpanRe.ReplaceAllFunc(body, func(m []byte) []byte {
			g := wikiSpanRe.FindSubmatch(m)
			display := string(g[2])
			if u := noteURL(unescapeAttr(string(g[1]))); u != "" {
				return []byte(`<a class="wikilink" href="` + esc(u) + `">` + display + `</a>`)
			}
			return []byte(`<span class="wikilink">` + display + `</span>`)
		})
	case "html":
		body = render.SanitizeHTML(doc.Body)
		body = wikilinkIDAttrRe.ReplaceAll(body, nil)
		body = anchorTagRe.ReplaceAllFunc(body, func(tag []byte) []byte {
			g := dataWikilinkRe.FindSubmatch(tag)
			if g == nil {
				return tag
			}
			u := noteURL(unescapeAttr(string(g[1])))
			if u == "" {
				return tag
			}
			return append([]byte(`<a href="`+esc(u)+`"`), tag[2:]...)
		})
	default:
		return nil, fmt.Errorf("note %s has unknown kind %q", n.ID, n.Kind)
	}
	if refs.Asset != nil {
		body = rewriteRefs(body, refs.Asset)
	}
	needs := needsOf(body)

	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n")
	b.WriteString("<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<meta name=\"robots\" content=\"noindex, nofollow\">\n")
	b.WriteString("<title>" + esc(n.Title) + "</title>\n")
	b.WriteString("<style>" + siteCSS + "</style>\n")
	if needs.math && refs.Rich != nil {
		if u := refs.Rich(siteKatexCSS); u != "" {
			b.WriteString("<link rel=\"stylesheet\" href=\"" + esc(u) + "\">\n")
		}
	}
	b.WriteString("</head>\n<body class=\"export-single\">\n")
	b.WriteString("<header class=\"x-header\"><span class=\"wordmark\">YANA/</span>")
	b.WriteString("<span class=\"x-meta\">shared note</span></header>\n")
	b.WriteString("<main class=\"x-main x-note\">\n")
	b.Write(body)
	b.WriteString("\n</main>\n")
	if refs.Rich != nil {
		if needs.mermaid {
			if u := refs.Rich(siteMermaidJS); u != "" {
				b.WriteString("<script src=\"" + esc(u) + "\"></script>\n")
			}
		}
		if needs.math {
			if u := refs.Rich(siteKatexJS); u != "" {
				b.WriteString("<script src=\"" + esc(u) + "\"></script>\n")
			}
		}
	}
	b.WriteString("</body>\n</html>\n")
	return []byte(b.String()), nil
}

// refTagRe matches the tags whose src or href can point beside the note.
var refTagRe = regexp.MustCompile(`(?s)<(?:img|a|video|audio|source)\b[^>]*>`)

// refAttrRe finds the src or href attribute inside one of those tags.
var refAttrRe = regexp.MustCompile(`\b(src|href)="([^"]*)"`)

// rewriteRefs points every relative src and href at what fn returns for
// it. External, absolute, fragment and data references are untouched,
// and so is anything fn declines.
func rewriteRefs(body []byte, fn func(rel string) string) []byte {
	return refTagRe.ReplaceAllFunc(body, func(tag []byte) []byte {
		m := refAttrRe.FindSubmatchIndex(tag)
		if m == nil {
			return tag
		}
		ref := html.UnescapeString(string(tag[m[4]:m[5]]))
		if ref == "" || isExternalRef(ref) {
			return tag
		}
		u := fn(ref)
		if u == "" {
			return tag
		}
		out := make([]byte, 0, len(tag)+len(u))
		out = append(out, tag[:m[4]]...)
		out = append(out, esc(u)...)
		out = append(out, tag[m[5]:]...)
		return out
	})
}
