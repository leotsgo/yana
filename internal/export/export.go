// Package export renders the tree out of the app: one self-contained
// HTML file per note, a static site per space or subtree, and a zip of
// the markdown and assets exactly as they sit on disk. This is the escape
// hatch behind invariant #6 — at any moment the user can walk away with
// the tree and lose nothing but edit history.
//
// Every path the package reads goes through pathsafe, and every export
// is scoped to one space, so a site or zip never carries a note the
// caller could not have read in the app.
package export

import (
	"context"
	"encoding/base64"
	"errors"
	"html"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
)

// Deps are everything an export needs. The DB supplies notes, links and
// backlinks; the Root is the path-safe way to the files themselves.
type Deps struct {
	DB   *index.DB
	Root *pathsafe.Root
	// SearchJS is the bundled client-side search runtime (minisearch
	// plus a small bootstrap) copied into static site exports. nil
	// builds a site without the search page.
	SearchJS []byte
	// Now stamps the exported pages; nil is time.Now.
	Now func() time.Time
}

// ErrNoNotes reports a space or subtree that holds nothing to export.
var ErrNoNotes = errors.New("no notes to export under that space or path")

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

// readNote reads one note's file and returns its parsed frontmatter doc.
func (d *Deps) readNote(n index.Note) (frontmatter.Doc, error) {
	abs, _, err := d.Root.Resolve(n.RelPath)
	if err != nil {
		return frontmatter.Doc{}, err
	}
	raw, err := os.ReadFile(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return frontmatter.Doc{}, index.ErrNotFound
	}
	if err != nil {
		return frontmatter.Doc{}, err
	}
	return frontmatter.Parse(raw), nil
}

// body returns the indexed text of one note: what the app's own search
// reads. Static site search indexes the same thing.
func (d *Deps) body(ctx context.Context, id string) string {
	b, err := d.DB.Body(ctx, id)
	if err != nil {
		return ""
	}
	return b
}

// pagePath maps a note's tree path, relative to its space, to its page
// in a static site: markdown notes trade their extension for .html,
// HTML notes already have one.
func pagePath(inSpace string) string {
	if strings.HasSuffix(strings.ToLower(inSpace), ".html") || strings.HasSuffix(strings.ToLower(inSpace), ".htm") {
		return inSpace
	}
	return strings.TrimSuffix(inSpace, path.Ext(inSpace)) + ".html"
}

// escPath escapes a slash path for use in an href, segment by segment.
func escPath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// relSlash expresses target relative to the directory dir ("." for the
// root). Both are clean slash paths within the site.
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
	return strings.Join(out, "/")
}

// hrefBetween returns the escaped href from the page at fromSitePath to
// the site path toSitePath.
func hrefBetween(fromSitePath, toSitePath string) string {
	return escPath(relSlash(path.Dir(fromSitePath), toSitePath))
}

// esc escapes a string for HTML text or a double-quoted attribute.
func esc(s string) string {
	return html.EscapeString(s)
}

// isExternalRef reports whether a src or href value points somewhere the
// exporter must not touch (scheme, absolute, fragment, or already data).
var schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

func isExternalRef(ref string) bool {
	return strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "#") ||
		strings.HasPrefix(ref, "/") || schemeRe.MatchString(ref)
}

// walkTree walks the files of a space or subtree under the root. baseRel
// is the subtree's path relative to the notes root (space prefix
// included). Dot directories are never entered; dot files reach fn with
// hidden=true so each export decides what travels. Each regular file is
// passed to fn with its path relative to the notes root.
func walkTree(root *pathsafe.Root, baseRel string, fn func(rel string, info fs.FileInfo, hidden bool)) error {
	base, _, err := root.Resolve(baseRel)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(base); err != nil || !fi.IsDir() {
		return ErrNoNotes
	}
	return fs.WalkDir(os.DirFS(base), ".", func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		hidden := strings.HasPrefix(de.Name(), ".")
		if de.IsDir() {
			if p != "." && hidden {
				return fs.SkipDir
			}
			return nil
		}
		info, err := de.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		rel := p
		if baseRel != "" {
			rel = baseRel + "/" + p
		}
		fn(rel, info, hidden)
		return nil
	})
}

// mimeExts maps asset extensions to media types.
var mimeExts = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".avif": "image/avif",
	".svg": "image/svg+xml", ".bmp": "image/bmp", ".ico": "image/x-icon",
}

// dataURI renders file bytes as a data: URI for inlining.
var base64Std = base64.StdEncoding

func dataURI(name string, data []byte) string {
	mime := mimeExts[strings.ToLower(path.Ext(name))]
	if mime == "" {
		if ct := http.DetectContentType(data); strings.HasPrefix(ct, "image/") {
			mime = ct
		} else {
			mime = "application/octet-stream"
		}
	}
	return "data:" + mime + ";base64," + base64Std.EncodeToString(data)
}
