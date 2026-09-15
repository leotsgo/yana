// HTML-note endpoints on the main origin: the short-lived view URL the
// client points a sandboxed frame at, save-based source editing with
// conflict copies, and the trust switch that opts a note out of
// sanitization.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/index"
)

// handleNoteView mints the URL the client loads in a sandboxed iframe:
// the content origin plus a token that opens exactly this note for
// ViewTTL. HTML notes only; markdown renders inline.
func (s *Server) handleNoteView(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.noteAuthz(w, r, id); !ok {
		return
	}
	if s.Content == nil {
		writeError(w, http.StatusNotImplemented, "this server runs without the content origin; set YANA_CONTENT_LISTEN")
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
	if n.Kind != "html" {
		writeError(w, http.StatusBadRequest, "only html notes render on the content origin")
		return
	}
	token, exp := s.Content.MintToken(id, ViewTTL)
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        s.contentBase(r) + "/n/" + id + "?token=" + token,
		"expires_at": exp,
	})
}

// contentBase is the public root of the content origin for one request:
// YANA_CONTENT_ORIGIN when it is set (a subdomain behind a proxy), and
// otherwise this request's host with the content listener's port.
func (s *Server) contentBase(r *http.Request) string {
	if s.ContentOrigin != "" {
		return strings.TrimSuffix(s.ContentOrigin, "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p == "http" || p == "https" {
		scheme = p
	}
	host := r.Host
	if _, port, ok := strings.Cut(host, ":"); ok {
		host = strings.TrimSuffix(host, ":"+port) + ":" + s.ContentPort()
	} else {
		host += ":" + s.ContentPort()
	}
	return scheme + "://" + host
}

// ContentPort is the port the content listener serves, for URL derivation.
func (s *Server) ContentPort() string {
	if s.ContentAddr == "" {
		return ""
	}
	if _, port, ok := strings.Cut(s.ContentAddr, ":"); ok && port != "" {
		return port
	}
	return strings.TrimPrefix(s.ContentAddr, ":")
}

// errNoteMissing aliases the index sentinel for the handlers here.
var errNoteMissing = index.ErrNotFound

// handleNoteSource saves an HTML note's source. Editing is save-based
// last-write-wins: the incoming text always lands, and when it does not
// build on what is on disk (the client's base hash is stale) the
// overwritten version is kept beside it as name.conflict-<ts>.html, which
// the scanner indexes like any other note.
func (s *Server) handleNoteSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	space, ok := s.noteAuthz(w, r, id)
	if !ok {
		return
	}
	n, err := s.DB.GetNote(r.Context(), id)
	if errors.Is(err, errNoteMissing) {
		writeError(w, http.StatusNotFound, "no note with that id")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if n.Kind != "html" {
		writeError(w, http.StatusBadRequest, "only html notes are edited by source save")
		return
	}
	if !s.mayWrite(w, r, space) {
		return
	}
	var body struct {
		Source   string `json:"source"`
		BaseHash string `json:"base_hash"`
	}
	limit := s.Root.Limits().MaxNoteSize + 1024
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with a source field")
		return
	}
	if int64(len(body.Source)) > s.Root.Limits().MaxNoteSize {
		writeError(w, http.StatusRequestEntityTooLarge, "content is over the note size limit")
		return
	}
	abs, rel, err := s.Root.Resolve(n.RelPath)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	current, err := os.ReadFile(abs)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "the note's file is gone; the index will catch up on the next scan")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}

	conflict := ""
	if body.BaseHash != "" && body.BaseHash != hashContent(current) {
		conflict, err = s.writeConflictCopy(abs, rel, current)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		loggerFrom(r.Context()).Warn("html note save over diverged file; conflict copy kept", "path", rel, "conflict", conflict)
	}

	// The frontmatter is the file's, not the editor's: id, created, and
	// any user keys ride along untouched above the new body.
	fm := frontmatter.Parse(current)
	next := make([]byte, 0, len(fm.Head)+len(body.Source))
	next = append(next, fm.Head...)
	next = append(next, body.Source...)
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(abs); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := fsutil.WriteFileAtomic(abs, next, mode); err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Scanner != nil {
		if err := s.Scanner.ScanOne(r.Context(), rel); err != nil {
			loggerFrom(r.Context()).Warn("saved note is not indexed yet", "path", rel, "err", err)
		}
	}
	if conflict != "" && s.Scanner != nil {
		if err := s.Scanner.ScanOne(r.Context(), conflict); err != nil {
			loggerFrom(r.Context()).Warn("conflict copy is not indexed yet", "path", conflict, "err", err)
		}
	}
	resp := map[string]any{
		"ok":   true,
		"path": rel,
		"hash": hashContent(next),
	}
	if conflict != "" {
		resp["conflict_copy"] = conflict
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeConflictCopy parks the diverged on-disk version beside the note as
// name.conflict-<ts>.html (-2, -3… if that name is taken). The copy ships
// without an id so the scanner treats it as the fresh note it is instead
// of a duplicate that steals the original's id.
func (s *Server) writeConflictCopy(abs, rel string, content []byte) (string, error) {
	if stripped, ok := frontmatter.RemoveKey(content, "id"); ok {
		content = stripped
	}
	dir, base := path.Split(rel)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	ts := time.Now().UTC().Format("20060102-150405")
	final := ""
	for i := 0; ; i++ {
		candidate := dir + stem + fmt.Sprintf(".conflict-%s", ts)
		if i > 0 {
			candidate += fmt.Sprintf("-%d", i+1)
		}
		candidate += ".html"
		clean, err := s.Root.Clean(candidate)
		if err != nil {
			return "", err
		}
		if err := s.Root.CheckCollision(clean); err != nil {
			continue
		}
		cAbs, _, err := s.Root.Resolve(clean)
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(cAbs); err == nil {
			continue
		}
		if err := fsutil.WriteFileAtomic(cAbs, content, 0o644); err != nil {
			return "", err
		}
		final = clean
		break
	}
	return final, nil
}

// handleNoteTrust flips the frontmatter's trusted flag. Trusting a note
// opts it out of sanitization on the content origin; untrusting it puts
// the sanitizer back in front of the very next render.
func (s *Server) handleNoteTrust(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	space, ok := s.noteAuthz(w, r, id)
	if !ok {
		return
	}
	n, err := s.DB.GetNote(r.Context(), id)
	if errors.Is(err, errNoteMissing) {
		writeError(w, http.StatusNotFound, "no note with that id")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if n.Kind != "html" {
		writeError(w, http.StatusBadRequest, "trusted applies to html notes")
		return
	}
	if !s.mayWrite(w, r, space) {
		return
	}
	var body struct {
		Trusted bool `json:"trusted"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	abs, rel, err := s.Root.Resolve(n.RelPath)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	current, err := os.ReadFile(abs)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "the note's file is gone; the index will catch up on the next scan")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	value := "false"
	if body.Trusted {
		value = "true"
	}
	next, _ := frontmatter.SetKey(current, "trusted", value)
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(abs); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := fsutil.WriteFileAtomic(abs, next, mode); err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Scanner != nil {
		if err := s.Scanner.ScanOne(r.Context(), rel); err != nil {
			loggerFrom(r.Context()).Warn("trust change is not indexed yet", "path", rel, "err", err)
		}
	}
	loggerFrom(r.Context()).Info("html note trust changed", "path", rel, "trusted", body.Trusted)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "trusted": body.Trusted})
}
