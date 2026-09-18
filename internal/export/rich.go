// Diagrams and math in exports. The renderer marks a mermaid fence as
// <pre class="mermaid"> and TeX as .math elements; a page that holds one
// needs the matching runtime. The web build ships both as classic
// scripts (they work from file://) under dist/export, which the server
// hands to Deps.Rich: a static site copies the files its pages use and
// links them, the single-file export inlines them, fonts included.
package export

import (
	"encoding/base64"
	"io/fs"
	"regexp"
	"strings"
)

// The runtime files as the web build lays them out.
const (
	richMermaidJS = "mermaid.js"
	richKatexJS   = "katex.js"
	richKatexCSS  = "katex-style.css"
	richFontDir   = "katex-fonts"
)

// Names the runtime files take in a static site. The stylesheet keeps
// its ./katex-fonts/ references, so the fonts sit beside it at the root.
const (
	siteMermaidJS = "mermaid.js"
	siteKatexJS   = "katex.js"
	siteKatexCSS  = "katex.css"
)

// richNeeds reports what a rendered body needs drawn.
type richNeeds struct {
	mermaid bool
	math    bool
}

func needsOf(body []byte) richNeeds {
	s := string(body)
	return richNeeds{
		mermaid: strings.Contains(s, `<pre class="mermaid">`),
		math:    strings.Contains(s, `<span class="math math-inline">`) || strings.Contains(s, `<div class="math math-display">`),
	}
}

func (n richNeeds) any() bool { return n.mermaid || n.math }

// richFile reads one runtime file, or nil when this build has none.
func (d *Deps) richFile(name string) []byte {
	if d.Rich == nil {
		return nil
	}
	b, err := fs.ReadFile(d.Rich, name)
	if err != nil {
		return nil
	}
	return b
}

// siteRichHead is the stylesheet link a site page needs, for its head.
func (d *Deps) siteRichHead(pagePath string, n richNeeds) string {
	if !n.math || d.richFile(richKatexCSS) == nil {
		return ""
	}
	return `<link rel="stylesheet" href="` + hrefBetween(pagePath, siteKatexCSS) + "\">\n"
}

// siteRichScripts are the script tags a site page needs, for the end of
// its body: the runtime runs once the page's markup is in place.
func (d *Deps) siteRichScripts(pagePath string, n richNeeds) string {
	var b strings.Builder
	if n.mermaid && d.richFile(richMermaidJS) != nil {
		b.WriteString(`<script src="` + hrefBetween(pagePath, siteMermaidJS) + "\"></script>\n")
	}
	if n.math && d.richFile(richKatexJS) != nil {
		b.WriteString(`<script src="` + hrefBetween(pagePath, siteKatexJS) + "\"></script>\n")
	}
	return b.String()
}

// fontURL matches the font references in KaTeX's stylesheet.
var fontURL = regexp.MustCompile(`url\("?\./` + richFontDir + `/([^")]+)"?\)`)

// inlineRich returns the style and script elements a single-file export
// embeds for what its body needs: the whole runtime, with the fonts
// folded into the stylesheet as data URIs, so the file still opens from
// anywhere with nothing beside it.
func (d *Deps) inlineRich(n richNeeds) (head, tail string) {
	var h, t strings.Builder
	if n.math {
		if css := d.richFile(richKatexCSS); css != nil {
			inlined := fontURL.ReplaceAllFunc(css, func(m []byte) []byte {
				name := string(fontURL.FindSubmatch(m)[1])
				data := d.richFile(richFontDir + "/" + name)
				if data == nil {
					return m
				}
				return []byte(`url("data:font/woff2;base64,` + base64.StdEncoding.EncodeToString(data) + `")`)
			})
			h.WriteString("<style>" + string(inlined) + "</style>\n")
		}
		if js := d.richFile(richKatexJS); js != nil {
			t.WriteString("<script>" + string(js) + "</script>\n")
		}
	}
	if n.mermaid {
		if js := d.richFile(richMermaidJS); js != nil {
			t.WriteString("<script>" + string(js) + "</script>\n")
		}
	}
	return h.String(), t.String()
}
