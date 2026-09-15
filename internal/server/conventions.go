// The per-space CONVENTIONS.md generator. The file is the contract an
// agent working against the bind-mounted tree reads before writing:
// frontmatter rules, wikilink syntax, the asset convention, and this
// space's actual folder structure.
package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/madeofpendletonwool/yana/internal/fsutil"
)

// conventionsName is the file this handler writes, at the space root.
const conventionsName = "CONVENTIONS.md"

// handleConventions writes or refreshes a space's CONVENTIONS.md. The
// body may ask for a refresh; without it an existing file is returned
// untouched, so regenerating is never accidental.
func (s *Server) handleConventions(w http.ResponseWriter, r *http.Request) {
	space, ok := s.spacePath(w, r)
	if !ok {
		return
	}
	if _, ok := s.spaceAuthz(w, r, space); !ok {
		return
	}
	if !s.mayWrite(w, r, space) {
		return
	}
	var body struct {
		Refresh bool `json:"refresh"`
	}
	// The body is optional; when there is one it must be JSON.
	if r.ContentLength != 0 {
		if err := decodeBody(w, r, &body); err != nil {
			return
		}
	}
	rel := space + "/" + conventionsName
	abs, _, err := s.Root.Resolve(rel)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !body.Refresh {
		if raw, err := os.ReadFile(abs); err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"path": rel, "created": false, "content": string(raw)})
			return
		}
	}
	folders := s.spaceFolders(space)
	doc := conventionsDoc(space, folders)
	if err := fsutil.WriteFileAtomic(abs, doc, 0o644); err != nil {
		s.fail(w, r, err)
		return
	}
	loggerFrom(r.Context()).Info("conventions written", "space", space)
	writeJSON(w, http.StatusCreated, map[string]any{"path": rel, "created": true, "content": string(doc)})
}

// spaceFolders walks the space's directory tree and returns its folder
// structure, two levels deep, as indented lines. Dotted directories and
// _assets are skipped: the first is invisible, the second is already
// documented as a rule.
func (s *Server) spaceFolders(space string) []string {
	base, _, err := s.Root.Resolve(space)
	if err != nil {
		return nil
	}
	var out []string
	var walk func(dir, prefix string, depth int)
	walk = func(dir, prefix string, depth int) {
		if depth > 2 || len(out) > 60 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "_assets" {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			out = append(out, prefix+name+"/")
			walk(filepath.Join(dir, name), prefix+"  ", depth+1)
		}
	}
	walk(base, "", 1)
	return out
}

// conventionsDoc renders the conventions file for a space.
func conventionsDoc(space string, folders []string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Conventions for %s\n\n", space)
	b.WriteString("How to write notes in this space so they fit the tree. This file is\n" +
		"for people and for agents working on the files directly; regenerate it with\n" +
		"POST /api/spaces/" + space + "/conventions after restructuring.\n\n")

	b.WriteString("## Files\n\n")
	b.WriteString("- A note is one markdown file ending in `.md`, in any folder of this space.\n" +
		"- Do not write an `id` or `created` key. The server assigns both the first time it\n" +
		"  sees a file and never changes them. Other frontmatter keys you add are carried\n" +
		"  through untouched.\n" +
		"- Write files completely and finish quickly, or write to a temporary name and\n" +
		"  `mv` into place; a file still being written is picked up after it settles.\n" +
		"- Files and folders starting with a dot are ignored.\n" +
		"- Attachments live in an `_assets` directory next to the note that uses them,\n" +
		"  referenced relatively: `![diagram](_assets/diagram.png)`.\n\n")

	b.WriteString("## Links\n\n")
	b.WriteString("- `[[Note title]]` links a note in this space by title; `[[folder/note|display text]]`\n" +
		"  links by path and shows its own display text.\n" +
		"- Links resolve within this space only. Every note lists its backlinks.\n" +
		"- Moving or renaming a note rewrites every inbound link, so link freely and\n" +
		"  rename without fear.\n\n")

	b.WriteString("## Folder structure\n\n")
	if len(folders) == 0 {
		b.WriteString("This space has no folders yet; create them to organise notes and regenerate\n" +
			"this file to record what they mean.\n")
	} else {
		for _, f := range folders {
			b.WriteString("- " + f + "\n")
		}
	}
	b.WriteString("\nA folder is a real directory; there is no ordering or tagging machinery in the\n" +
		"tree itself. Nesting is fine within the usual path limits.\n")
	return []byte(b.String())
}
