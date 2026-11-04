// Embed inlining for the read view. The markdown renderer leaves one
// span per ![[target]]; this pass resolves each target within the
// note's space and replaces the span with the target's body under a
// small header, once and without recursion: an embed inside an embedded
// body renders as a plain wikilink, and a note embedding itself renders
// a link. Unresolved embeds become the ordinary create affordance.
package server

import (
	"context"
	"html"
	"path"
	"regexp"
	"strings"

	"github.com/madeofpendletonwool/yana/internal/drawing"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/render"
)

// embedSpanRe matches the spans the renderer emits for ![[targets]].
var embedSpanRe = regexp.MustCompile(`<span class="wikiembed" data-target="([^"]*)">[^<]*</span>`)

// inlineEmbeds replaces every embed span in body with its target's
// rendered body. from is the note the body belongs to.
func (s *Server) inlineEmbeds(ctx context.Context, from index.Note, body []byte) []byte {
	if !strings.Contains(string(body), "wikiembed") {
		return body
	}
	res, err := s.DB.SpaceResolver(ctx, from.Space)
	if err != nil {
		return embedsAsLinks(body)
	}
	fromRel := from.RelPath
	fromDir := path.Dir(fromRel)
	return embedSpanRe.ReplaceAllFunc(body, func(m []byte) []byte {
		g := embedSpanRe.FindSubmatch(m)
		raw := html.UnescapeString(string(g[1]))
		got := res.Resolve(raw, fromRel)
		if !got.OK || got.ToID == from.ID {
			return wikilinkSpan(raw)
		}
		target, err := s.DB.GetNote(ctx, got.ToID)
		if err == nil && target.Kind == drawing.Kind {
			// The client mounts the drawing read-only in this div.
			return []byte(`<div class="drawing-embed" data-id="` + html.EscapeString(target.ID) + `">` +
				string(wikilinkSpanFor(raw, target.Title)) + `</div>`)
		}
		if err != nil || target.Kind != "md" {
			return wikilinkSpan(raw)
		}
		src, err := s.DB.RawBody(ctx, target.ID)
		if err != nil {
			return wikilinkSpan(raw)
		}
		inner, err := render.Markdown([]byte(src))
		if err != nil {
			return wikilinkSpan(raw)
		}
		// One level only: embeds inside the embedded body are links.
		inner = embedSpanRe.ReplaceAllFunc(inner, func(m2 []byte) []byte {
			g2 := embedSpanRe.FindSubmatch(m2)
			return wikilinkSpan(html.UnescapeString(string(g2[1])))
		})
		// Images inside the embedded body resolve against the target's
		// directory; restate them relative to the embedding note so the
		// client's pass computes the right path.
		inner = rebaseImages(inner, path.Dir(target.RelPath), fromDir)
		var b strings.Builder
		b.WriteString(`<section class="embed"><div class="embed-head">`)
		b.Write(wikilinkSpanFor(raw, target.Title))
		b.WriteString(`</div><div class="embed-body">`)
		b.Write(inner)
		b.WriteString(`</div></section>`)
		return []byte(b.String())
	})
}

// embedsAsLinks turns every embed span into a plain wikilink span, for
// when resolution is not available at all.
func embedsAsLinks(body []byte) []byte {
	return embedSpanRe.ReplaceAllFunc(body, func(m []byte) []byte {
		g := embedSpanRe.FindSubmatch(m)
		return wikilinkSpan(html.UnescapeString(string(g[1])))
	})
}

func wikilinkSpan(raw string) []byte {
	return wikilinkSpanFor(raw, raw)
}

func wikilinkSpanFor(raw, display string) []byte {
	esc := html.EscapeString(raw)
	return []byte(`<span class="wikilink" data-target="` + esc + `">` + html.EscapeString(display) + `</span>`)
}

// imgSrcRe finds image source attributes in rendered HTML.
var imgSrcRe = regexp.MustCompile(`(?s)(<img\b[^>]*?\bsrc=")([^"]*)("[^>]*>)`)

// rebaseImages rewrites relative image srcs that resolve against baseDir
// so they resolve against toDir instead, as relative paths with .. where
// needed. Anything already absolute or remote is left alone.
func rebaseImages(body []byte, baseDir, toDir string) []byte {
	return imgSrcRe.ReplaceAllFunc(body, func(m []byte) []byte {
		g := imgSrcRe.FindSubmatch(m)
		src := html.UnescapeString(string(g[2]))
		if src == "" || strings.Contains(src, "://") || strings.HasPrefix(src, "/") || strings.HasPrefix(src, "data:") {
			return m
		}
		abs := path.Join(baseDir, src)
		return append(append(append([]byte{}, g[1]...), []byte(html.EscapeString(relPathFrom(toDir, abs)))...), g[3]...)
	})
}

// relPathFrom expresses target as a path relative to dir, with .. where
// needed; both are clean slash paths inside the tree.
func relPathFrom(dir, target string) string {
	if dir == "." || dir == "" {
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
	if len(out) == 0 {
		return "."
	}
	return path.Join(out...)
}
