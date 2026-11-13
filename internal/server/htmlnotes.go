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

	"github.com/leotsgo/yana/internal/drawing"
	"github.com/leotsgo/yana/internal/frontmatter"
	"github.com/leotsgo/yana/internal/fsutil"
	"github.com/leotsgo/yana/internal/index"
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

// handleNoteSource saves an HTML note's or a drawing's source. An HTML
// note is save-based last-write-wins: the incoming text always lands, and
// when it does not build on what is on disk (the client's base hash is
// stale) the overwritten version is kept beside it as
// name.conflict-<ts>.html, which the scanner indexes like any other note.
// A drawing merges with the disk instead (drawing.Merge); when the merge
// brought in elements the client lacked, the reply carries the scene.
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
	if n.Kind != "html" && n.Kind != drawing.Kind {
		writeError(w, http.StatusBadRequest, "only html and drawing notes are edited by source save")
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
	text := []byte(body.Source)
	if n.Kind == drawing.Kind {
		if drawing.Validate(text) != nil {
			writeError(w, http.StatusBadRequest, "source is not an excalidraw scene")
			return
		}
		text = drawing.Encode(text)
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

	// The frontmatter is the file's, not the editor's: id, created, and
	// any user keys ride along untouched above the new body.
	// A drawing merges, element by element, with what is on disk: a save
	// from a stale base (another device, an offline draft) keeps both
	// sides' strokes instead of parking one in a conflict copy. A drawing
	// that does not parse is kept as a conflict copy whatever the base
	// hash says: the save must never destroy what it cannot read.
	merged, conflict := "", ""
	var next []byte
	if n.Kind == drawing.Kind {
		// Read again under the lock Rewrite holds: a save that finished
		// meanwhile is merged too, not overwritten.
		var reject *saveReject
		_, next, err = drawing.Rewrite(abs, func(file []byte) ([]byte, error) {
			fm := frontmatter.Parse(file)
			unreadable := false
			if _, disk, perr := drawing.Parse(fm.Body); perr != nil {
				unreadable = true
			} else {
				scene, changed, err := drawing.Merge(disk, []byte(body.Source))
				switch {
				case errors.Is(err, drawing.ErrIncoming):
					return nil, &saveReject{http.StatusBadRequest, "source is not an excalidraw scene"}
				case err != nil:
					unreadable = true
				case changed:
					merged = string(scene)
					text = drawing.Encode(scene)
				}
			}
			out := append(append(make([]byte, 0, len(fm.Head)+len(text)), fm.Head...), text...)
			if int64(len(out)) > s.Root.Limits().MaxNoteSize {
				return nil, &saveReject{http.StatusRequestEntityTooLarge, "content is over the note size limit"}
			}
			if unreadable {
				var cerr error
				if conflict, cerr = s.writeConflictCopy(abs, rel, file); cerr != nil {
					return nil, cerr
				}
				loggerFrom(r.Context()).Warn(n.Kind+" note save over diverged file; conflict copy kept", "path", rel, "conflict", conflict)
			}
			return out, nil
		})
		if errors.As(err, &reject) {
			writeError(w, reject.status, reject.msg)
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
	} else {
		fm := frontmatter.Parse(current)
		next = make([]byte, 0, len(fm.Head)+len(text))
		next = append(next, fm.Head...)
		next = append(next, text...)
		if int64(len(next)) > s.Root.Limits().MaxNoteSize {
			writeError(w, http.StatusRequestEntityTooLarge, "content is over the note size limit")
			return
		}
		if body.BaseHash != "" && body.BaseHash != hashContent(current) {
			conflict, err = s.writeConflictCopy(abs, rel, current)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			loggerFrom(r.Context()).Warn(n.Kind+" note save over diverged file; conflict copy kept", "path", rel, "conflict", conflict)
		}
		mode := fs.FileMode(0o644)
		if fi, err := os.Stat(abs); err == nil {
			mode = fi.Mode().Perm()
		}
		if err := fsutil.WriteFileAtomic(abs, next, mode); err != nil {
			s.fail(w, r, err)
			return
		}
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
	if merged != "" {
		// The client's scene lacked what the disk held: it takes this one.
		resp["scene"] = merged
	}
	writeJSON(w, http.StatusOK, resp)
}

// saveReject is a refusal the save's edit hands back through
// drawing.Rewrite so the handler can answer with the right status.
type saveReject struct {
	status int
	msg    string
}

func (e *saveReject) Error() string { return e.msg }

// writeConflictCopy parks the diverged on-disk version beside the note as
// name.conflict-<ts>.<ext> (-2, -3… if that name is taken). The copy ships
// without an id so the scanner treats it as the fresh note it is instead
// of a duplicate that steals the original's id.
func (s *Server) writeConflictCopy(abs, rel string, content []byte) (string, error) {
	if stripped, ok := frontmatter.RemoveKey(content, "id"); ok {
		content = stripped
	}
	dir, base := path.Split(rel)
	stem, ext := drawing.SplitExt(base)
	ts := time.Now().UTC().Format("20060102-150405")
	final := ""
	for i := 0; ; i++ {
		candidate := dir + stem + fmt.Sprintf(".conflict-%s", ts)
		if i > 0 {
			candidate += fmt.Sprintf("-%d", i+1)
		}
		candidate += ext
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
