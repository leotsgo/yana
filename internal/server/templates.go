// Templates: notes in a space's templates/ folder, the way the daily
// note's templates/daily.md already works. Any note there is a template;
// its frontmatter is dropped on use and the new note gets an id of its
// own. This file holds the one endpoint the client needs: expand a
// template — substitute its variables, report the prompts it asks and
// where the caret lands — without writing anything. Creating the note is
// POST /api/notes with the expanded body, so permissions and the id stay
// where they already are.
package server

import (
	"errors"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/templates"
)

// TemplatesFolder is the folder, in every space, whose notes are
// templates. It is a normal folder: templates are edited like notes,
// sync and export with everything else.
const TemplatesFolder = "templates"

// isTemplatePath reports whether a note sits somewhere under a space's
// templates/ folder.
func isTemplatePath(rel string) bool {
	for _, seg := range strings.Split(path.Dir(rel), "/") {
		if seg == TemplatesFolder {
			return true
		}
	}
	return false
}

// templateBody reads a template note's body, frontmatter dropped. ok is
// false when the file is not there or cannot be read; callers fall back
// to their own seed, the way the daily note does.
func (s *Server) templateBody(rel string) (body []byte, ok bool) {
	abs, _, err := s.Root.Resolve(rel)
	if err != nil {
		return nil, false
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, false
	}
	return bodyOf(raw), true
}

// handleTemplateExpand (POST /api/templates/{id}/expand) substitutes a
// template's variables and answers with the body, where the caret lands,
// the prompts it asks, and what the template's own name suggests as the
// new note's title. Nothing is written: the caller creates the note (or
// inserts the body into an open one) with what comes back.
//
// The request is {title, folder, answers}: the new note's title, the
// folder it will land in (a full path from the tree root; the template's
// own space when empty), and the answers to the prompts, by label. A
// first call with no answers learns the prompts; a second with them gets
// the finished body.
func (s *Server) handleTemplateExpand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	space, _, ok := s.noteRole(w, r, id)
	if !ok {
		return
	}
	n, err := s.DB.GetNote(r.Context(), id)
	if errors.Is(err, index.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no note with that id")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if n.Kind != "md" || !isTemplatePath(n.RelPath) {
		writeError(w, http.StatusBadRequest, "that note is not a template; templates live in a space's templates/ folder")
		return
	}
	body, ok := s.templateBody(n.RelPath)
	if !ok {
		writeError(w, http.StatusNotFound, "the template's file is gone; the index will catch up on the next scan")
		return
	}
	var req struct {
		Title   string            `json:"title"`
		Folder  string            `json:"folder"`
		Answers map[string]string `json:"answers"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		return
	}
	// The folder the new note lands in decides {{space}} and {{folder}};
	// with none given, the template's own space holds the note. The
	// strings only echo back what the caller sent, so no further
	// membership check runs here.
	if strings.TrimSpace(req.Folder) != "" {
		clean, err := s.Root.Clean(req.Folder)
		if err != nil || clean == "" || isAssetPath(clean) {
			writeError(w, http.StatusBadRequest, "folder must be a folder inside the tree")
			return
		}
		if i := strings.IndexByte(clean, '/'); i >= 0 {
			space, req.Folder = clean[:i], clean[i+1:]
		} else {
			space, req.Folder = clean, ""
		}
	}
	values := templates.Values{
		Title:   req.Title,
		When:    time.Now(),
		User:    s.ident(r).Username,
		Space:   space,
		Folder:  req.Folder,
		Answers: req.Answers,
	}
	res := templates.Apply(string(body), values)
	// The template's own name, with its variables filled, suggests the
	// new note's title: a template called "Meeting {{date}}" suggests
	// "Meeting 2026-09-29". {{title}} has no answer yet, so it drops out.
	nameVals := values
	nameVals.Title = ""
	name := path.Base(n.RelPath)
	suggest := templates.Apply(strings.TrimSuffix(name, path.Ext(name)), nameVals).Body
	suggest = strings.TrimSpace(strings.ReplaceAll(suggest, "{{title}}", ""))
	writeJSON(w, http.StatusOK, map[string]any{
		"body":    res.Body,
		"cursor":  res.Cursor,
		"prompts": res.Prompts,
		"suggest": suggest,
	})
}
