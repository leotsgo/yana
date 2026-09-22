// Endpoints behind the editor: asset upload for drag-and-drop and paste,
// the daily note, and markdown rendering for the live preview.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	neturl "net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/render"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// DailyConfig is where the daily note lives and what seeds it. Both paths
// are relative to a space and expand {YYYY}, {MM}, {DD} and {date}.
type DailyConfig struct {
	Pattern  string
	Template string
}

// DefaultDaily matches the config package defaults.
var DefaultDaily = DailyConfig{
	Pattern:  "journal/{YYYY}/{MM}/{YYYY}-{MM}-{DD}.md",
	Template: "templates/daily.md",
}

// daily is the effective daily-note configuration: the defaults when
// nothing was set, and the default pattern when only the template was.
func (s *Server) daily() DailyConfig {
	cfg := s.Daily
	if cfg == (DailyConfig{}) {
		return DefaultDaily
	}
	if cfg.Pattern == "" {
		cfg.Pattern = DefaultDaily.Pattern
	}
	return cfg
}

// --- asset upload -------------------------------------------------------

// handleFileUpload writes one file under an _assets directory. The body is
// the raw file; the path names where it goes. A name already in use gets a
// numeric suffix instead of being overwritten, and the response carries
// the path that was actually written so the client can link it.
func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	clean, err := s.Root.Clean(r.PathValue("path"))
	if err != nil || clean == "" {
		writeError(w, http.StatusBadRequest, "that path is not allowed")
		return
	}
	if !isAssetPath(clean) {
		writeError(w, http.StatusBadRequest, "uploads go under an _assets directory next to the note")
		return
	}
	if scanner.KindOf(clean) != "" {
		writeError(w, http.StatusBadRequest, "notes are created through POST /api/notes, not uploaded")
		return
	}
	space := spaceOfPath(clean)
	if !s.mayWrite(w, r, space) {
		return
	}
	limit := s.Root.Limits().MaxAssetSize
	if r.ContentLength > limit {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file is over the asset size limit (%d bytes)", limit))
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file is over the asset size limit (%d bytes)", limit))
			return
		}
		writeError(w, http.StatusBadRequest, "could not read the upload")
		return
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "the upload is empty")
		return
	}
	abs, _, err := s.Root.Resolve(clean)
	if err != nil {
		if pathsafe.IsRejection(err) {
			writeError(w, http.StatusBadRequest, "that path is not allowed")
			return
		}
		s.fail(w, r, err)
		return
	}
	if err := fsutil.MkdirInherit(filepath.Dir(abs)); err != nil {
		s.fail(w, r, err)
		return
	}
	// Pick a free name: name.png, name-2.png, name-3.png, ...
	dir, base := path.Split(clean)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	final := clean
	for i := 2; ; i++ {
		if _, err := os.Lstat(filepath.Join(s.Root.Dir(), filepath.FromSlash(final))); errors.Is(err, fs.ErrNotExist) {
			if err := s.Root.CheckCollision(final); err == nil {
				break
			}
		}
		if i > 1000 {
			writeError(w, http.StatusConflict, "could not find a free name for the file")
			return
		}
		final = dir + fmt.Sprintf("%s-%d%s", stem, i, ext)
	}
	abs, _, err = s.Root.Resolve(final)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := fsutil.WriteFileAtomic(abs, data, 0o644); err != nil {
		s.fail(w, r, err)
		return
	}
	// Index the file right away (asset row, PDF text and page count), so
	// the card and search see it without waiting for the next scan.
	if s.Scanner != nil {
		if err := s.Scanner.ScanAsset(r.Context(), final); err != nil {
			loggerFrom(r.Context()).Warn("uploaded asset is not indexed yet", "path", final, "err", err)
		}
	}
	resp := map[string]any{
		"path": final,
		"name": path.Base(final),
		"size": len(data),
		"url":  "/api/files/" + strings.Join(encodeSegments(final), "/"),
	}
	if scanner.IsPDF(final) {
		if att, err := s.DB.GetAttachment(r.Context(), final); err == nil && att.Pages != nil {
			resp["pages"] = *att.Pages
		}
	}
	loggerFrom(r.Context()).Info("asset uploaded", "path", final, "bytes", len(data))
	writeJSON(w, http.StatusCreated, resp)
}

func encodeSegments(rel string) []string {
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		parts[i] = neturl.PathEscape(p)
	}
	return parts
}

// --- daily note ---------------------------------------------------------

var dateRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)

// expandDaily fills the date tokens in a pattern or template body.
func expandDaily(s string, day time.Time) string {
	r := strings.NewReplacer(
		"{YYYY}", day.Format("2006"),
		"{MM}", day.Format("01"),
		"{DD}", day.Format("02"),
		"{date}", day.Format("2006-01-02"),
	)
	return r.Replace(s)
}

// handleDailyNote opens today's note: it answers with the existing one
// when the file is there and creates it from the template otherwise. The
// client sends its local date so "today" is the user's, not the
// server's.
func (s *Server) handleDailyNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Space string `json:"space"`
		Date  string `json:"date"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with space and date fields")
		return
	}
	day, err := time.Parse("2006-01-02", body.Date)
	if err != nil || !dateRe.MatchString(body.Date) {
		writeError(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	space := ""
	if strings.TrimSpace(body.Space) != "" {
		space, err = s.Root.Clean(body.Space)
		if err != nil || strings.Contains(space, "/") {
			writeError(w, http.StatusBadRequest, "space must be a single directory name")
			return
		}
	}
	cfg := s.daily()
	rel := expandDaily(cfg.Pattern, day)
	if space != "" {
		rel = space + "/" + rel
	}
	clean, err := s.Root.Clean(rel)
	if err != nil || scanner.KindOf(clean) == "" || scanner.IsAsset(clean) {
		writeError(w, http.StatusBadRequest, "the daily note pattern does not name a markdown note")
		return
	}
	if !s.mayWrite(w, r, space) {
		return
	}
	if n, err := s.DB.GetNoteByPath(r.Context(), clean); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"id": n.ID, "path": n.RelPath, "note": n, "created": false})
		return
	}
	abs, _, err := s.Root.Resolve(clean)
	if err != nil {
		if pathsafe.IsRejection(err) {
			writeError(w, http.StatusBadRequest, "that path is not allowed")
			return
		}
		s.fail(w, r, err)
		return
	}
	if _, err := os.Lstat(abs); err == nil {
		// On disk but not indexed yet: let the scanner catch up rather
		// than racing it.
		if s.Scanner != nil {
			_ = s.Scanner.ScanOne(r.Context(), clean)
		}
		if n, err := s.DB.GetNoteByPath(r.Context(), clean); err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"id": n.ID, "path": n.RelPath, "note": n, "created": false})
			return
		}
		writeError(w, http.StatusConflict, "today's note exists but is not indexed yet; try again in a moment")
		return
	}
	if err := s.Root.CheckCollision(clean); err != nil {
		writeError(w, http.StatusBadRequest, "a note with a name equal after case folding exists here")
		return
	}
	seed := "# " + day.Format("2006-01-02") + "\n\n"
	if cfg.Template != "" {
		tpl := expandDaily(cfg.Template, day)
		if space != "" {
			tpl = space + "/" + tpl
		}
		if tclean, err := s.Root.Clean(tpl); err == nil && tclean != "" {
			if tabs, _, err := s.Root.Resolve(tclean); err == nil {
				if raw, err := os.ReadFile(tabs); err == nil {
					seed = expandDaily(string(bodyOf(raw)), day)
				}
			}
		}
	}
	id := scanner.NewID(time.Now())
	content, _, _ := frontmatter.EnsureID([]byte(seed), id, time.Now())
	if err := fsutil.MkdirInherit(filepath.Dir(abs)); err != nil {
		s.fail(w, r, err)
		return
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if os.IsExist(err) {
		writeError(w, http.StatusConflict, "a file already exists at that path")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		s.fail(w, r, err)
		return
	}
	f.Close()
	if s.Scanner != nil {
		if err := s.Scanner.ScanOne(r.Context(), clean); err != nil {
			loggerFrom(r.Context()).Warn("daily note is not indexed yet", "path", clean, "err", err)
		}
	}
	n, err := s.DB.GetNoteByPath(r.Context(), clean)
	if err != nil {
		writeJSON(w, http.StatusCreated, map[string]any{"id": id, "path": clean, "created": true})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": n.ID, "path": n.RelPath, "note": n, "created": true})
}

// --- live preview -------------------------------------------------------

// handleRender turns markdown into the same HTML the note endpoint
// produces, so the editor's preview matches the read view. The body is
// the current editor text, which may be ahead of the file on disk.
func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Markdown string `json:"markdown"`
	}
	limit := s.Root.Limits().MaxNoteSize + 1024
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with a markdown field")
		return
	}
	if int64(len(body.Markdown)) > s.Root.Limits().MaxNoteSize {
		writeError(w, http.StatusRequestEntityTooLarge, "content is over the note size limit")
		return
	}
	html, err := render.Markdown(bodyOf([]byte(body.Markdown)))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"html": string(html)})
}
