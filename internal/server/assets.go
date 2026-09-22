// Asset endpoints: metadata for the read view's attachment cards, the
// orphan listing on the Data page, and the move-to-trash action for
// assets. Serving the bytes themselves lives in server.go (the app
// origin) and content.go (the content origin); the MIME set both share
// is decided here.
package server

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// assetInline maps the extensions served inline: images, PDFs, and plain
// text. Everything else — office documents in particular — is served as
// application/octet-stream with an attachment disposition, so the browser
// downloads instead of rendering.
var assetInline = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".avif": "image/avif",
	".svg": "image/svg+xml", ".bmp": "image/bmp", ".ico": "image/x-icon",
	".pdf": "application/pdf",
	".txt": "text/plain; charset=utf-8", ".csv": "text/plain; charset=utf-8",
	".md": "text/plain; charset=utf-8", ".log": "text/plain; charset=utf-8",
}

// applyAssetHeaders sets the content type and disposition for one asset
// before ServeContent, so what a browser does with the file is decided
// here and not by extension sniffing.
func applyAssetHeaders(w http.ResponseWriter, name string) {
	ct := assetInline[strings.ToLower(path.Ext(name))]
	inline := ct != ""
	if !inline {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	disposition := "inline"
	if !inline {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": path.Base(name)}))
}

// handleAttachmentGet answers one attachment's metadata for the card the
// read view shows: name, size, and the PDF page count once the scanner
// has extracted it.
func (s *Server) handleAttachmentGet(w http.ResponseWriter, r *http.Request) {
	abs, rel, err := s.Root.Resolve(r.PathValue("path"))
	if err != nil || !isAssetPath(rel) {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	if _, ok := s.spaceAuthz(w, r, spaceOfPath(rel)); !ok {
		return
	}
	fi, err := os.Stat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	if err != nil || !fi.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	out := map[string]any{
		"path": rel,
		"name": path.Base(rel),
		"size": fi.Size(),
		"kind": "file",
		"url":  "/api/files/" + strings.Join(encodeSegments(rel), "/"),
	}
	if scanner.IsPDF(rel) {
		out["kind"] = "pdf"
		if att, err := s.DB.GetAttachment(r.Context(), rel); err == nil && att.Pages != nil {
			out["pages"] = *att.Pages
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAssetOrphans lists assets no note in their space references, for
// the Data page. Only spaces the caller can read.
func (s *Server) handleAssetOrphans(w http.ResponseWriter, r *http.Request) {
	assets, err := s.DB.OrphanAssets(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	allow := s.trashAllow(r)
	out := make([]index.Asset, 0, len(assets))
	for _, a := range assets {
		if allow != nil && !allow(a.Space) {
			continue
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"assets": out})
}

// handleAssetTrash moves one asset to the trash (never a delete), the
// same .trash/ conventions as notes.
func (s *Server) handleAssetTrash(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "this server runs without the reconciliation loop")
		return
	}
	_, rel, err := s.Root.Resolve(r.PathValue("path"))
	if err != nil || !isAssetPath(rel) {
		writeError(w, http.StatusBadRequest, "that path is not allowed")
		return
	}
	trashPath, err := s.Sync.TrashAsset(r.Context(), rel, s.canWrite(w, r))
	if err != nil {
		var denied *reconcile.SpaceDeniedError
		if errors.As(err, &denied) {
			writeError(w, http.StatusForbidden, denied.Unwrap().Error())
			return
		}
		if errors.Is(err, reconcile.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no such file")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "trash_path": trashPath})
}

// handleNoteViewAsset mints the content-origin URL an attachment card
// expands into: a view token scoped to the note beside the file, pointed
// at the asset route. The PDF then renders on the content origin under
// the same policy as HTML notes, never in the app's origin.
func (s *Server) handleNoteViewAsset(w http.ResponseWriter, r *http.Request) {
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
	clean, err := s.Root.Clean(r.URL.Query().Get("asset"))
	if err != nil || clean == "" || !isAssetPath(clean) {
		writeError(w, http.StatusBadRequest, "asset must name a file under an _assets directory")
		return
	}
	if spaceOfPath(clean) != n.Space {
		writeError(w, http.StatusForbidden, "this file is not in the note's space")
		return
	}
	token, exp := s.Content.MintToken(id, ViewTTL)
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        s.contentBase(r) + "/t/" + token + "/f/" + strings.Join(encodeSegments(clean), "/"),
		"expires_at": exp,
	})
}
