// The single-file export: one note as one self-contained HTML document.
// Images that live beside the note are inlined as data URIs and the
// stylesheet is embedded, so the file opens anywhere — mail it, archive
// it, drop it in a browser with no network and it reads the same as in
// the app.
package export

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/render"
)

// SingleNote renders one note as a self-contained HTML document. Wikilinks
// stay as plain spans: a single file has nowhere to link to.
func (d *Deps) SingleNote(ctx context.Context, id string) ([]byte, error) {
	n, err := d.DB.GetNote(ctx, id)
	if errors.Is(err, index.ErrNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	doc, err := d.readNote(n)
	if err != nil {
		return nil, err
	}
	var body []byte
	switch n.Kind {
	case "md":
		body, err = render.Markdown(doc.Body)
		if err != nil {
			return nil, err
		}
	case "html":
		body = doc.Body
		if !doc.Meta.Trusted {
			body = render.SanitizeHTML(body)
		}
	default:
		return nil, fmt.Errorf("note %s has unknown kind %q", id, n.Kind)
	}
	body = d.inlineImages(body, path.Dir(n.RelPath), n)
	richHead, richTail := d.inlineRich(needsOf(body))

	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n")
	b.WriteString("<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<title>" + esc(n.Title) + " — YANA/</title>\n")
	icon := ""
	if len(d.Favicon) > 0 {
		icon = "data:image/png;base64," + base64.StdEncoding.EncodeToString(d.Favicon)
		b.WriteString("<link rel=\"icon\" type=\"image/png\" href=\"" + icon + "\">\n")
	}
	b.WriteString("<style>" + siteCSS + "</style>\n")
	b.WriteString(richHead)
	b.WriteString("</head>\n<body class=\"export-single\">\n")
	b.WriteString("<header class=\"x-header\"><span class=\"wordmark\">" + markImg(icon) + "YANA/</span>")
	b.WriteString("<span class=\"x-meta\">" + esc(n.RelPath) + " · exported " + d.now().Format("2006-01-02") + "</span></header>\n")
	b.WriteString("<main class=\"x-main x-note\">\n")
	b.Write(body)
	b.WriteString("\n</main>\n")
	b.WriteString(richTail)
	b.WriteString("</body>\n</html>\n")
	return []byte(b.String()), nil
}

// imgTagRe matches whole <img> tags in rendered output; srcAttrRe finds
// the src attribute inside one.
var (
	imgTagRe  = regexp.MustCompile(`(?s)<img\b[^>]*>`)
	srcAttrRe = regexp.MustCompile(`\bsrc="([^"]*)"`)
)

// inlineImages replaces relative image sources with data URIs read from
// the tree. Anything that cannot be read — an external URL, a missing
// file, an oversized asset — keeps its original src.
func (d *Deps) inlineImages(body []byte, noteDir string, n index.Note) []byte {
	return imgTagRe.ReplaceAllFunc(body, func(tag []byte) []byte {
		m := srcAttrRe.FindSubmatchIndex(tag)
		if m == nil {
			return tag
		}
		src := html.UnescapeString(string(tag[m[2]:m[3]]))
		if src == "" || isExternalRef(src) {
			return tag
		}
		abs, _, err := d.Root.Resolve(joinSlash(noteDir, stripRef(src)))
		if err != nil {
			return tag
		}
		fi, err := os.Stat(abs)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > d.Root.Limits().MaxAssetSize {
			return tag
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return tag
		}
		uri := []byte(`src="` + dataURI(fi.Name(), data) + `"`)
		out := make([]byte, 0, len(tag)+len(uri))
		out = append(out, tag[:m[0]]...)
		out = append(out, uri...)
		out = append(out, tag[m[1]:]...)
		return out
	})
}

// joinSlash joins two slash paths, treating "." and "" as the root.
func joinSlash(dir, rel string) string {
	rel = strings.TrimPrefix(rel, "./")
	if dir == "" || dir == "." {
		return rel
	}
	return dir + "/" + rel
}

// stripRef drops a query or fragment from a relative reference.
func stripRef(src string) string {
	if i := strings.IndexAny(src, "?#"); i >= 0 {
		return src[:i]
	}
	return src
}
