// Endpoints behind the editor: asset upload for drag-and-drop and paste,
// the daily note, and markdown rendering for the live preview.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/render"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/templates"
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
	final, ok := s.Root.FreeName(clean)
	if !ok {
		writeError(w, http.StatusConflict, "could not find a free name for the file")
		return
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

// folderOfDaily is the folder the daily note lands in, relative to its
// space: what {{folder}} says in a daily template.
func folderOfDaily(pattern, space string, day time.Time) string {
	dir := path.Dir(expandDaily(pattern, day))
	if space != "" {
		dir = strings.TrimPrefix(dir, space+"/")
	}
	return dir
}

// inFolder moves a daily pattern into a folder a person chose: the
// leading folders that carry no date token give way to it, the dated
// part stays. journal/{YYYY}/{MM}/{date}.md in "daily" becomes
// daily/{YYYY}/{MM}/{date}.md; {date}.md becomes daily/{date}.md; an
// empty folder is the top of the space. The web client's dailyPath
// (web/src/defaults.ts) mirrors this for the settings preview.
func inFolder(pattern, folder string) string {
	parts := strings.Split(pattern, "/")
	i := 0
	for i < len(parts)-1 && !strings.Contains(parts[i], "{") {
		i++
	}
	rest := strings.Join(parts[i:], "/")
	if folder == "" {
		return rest
	}
	return folder + "/" + rest
}

// handleDailyNote opens today's note: it answers with the existing one
// when the file is there and creates it from the template otherwise. The
// client sends its local date so "today" is the user's, not the
// server's.
func (s *Server) handleDailyNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Space string `json:"space"`
		Date  string `json:"date"`
		// Folder, inside the space, takes the place of the pattern's
		// leading folders; "" is the top of the space, and leaving it
		// out keeps the pattern as configured.
		Folder *string `json:"folder"`
		// ID names the note when it has to be made (see clientNoteID);
		// an existing note at the path is answered whatever it says.
		ID string `json:"id"`
		// Content, when present, is the new note's body in place of the
		// template: a client that made the note offline sends "" and
		// brings the text through the note's CRDT document instead.
		Content *string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with space and date fields")
		return
	}
	if body.ID != "" && !validULID(body.ID) {
		writeError(w, http.StatusBadRequest, "id must be a 26-character upper-case ULID")
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
	if body.Folder != nil {
		f, err := s.Root.Clean(strings.Trim(strings.TrimSpace(*body.Folder), "/"))
		if err != nil || space == "" {
			writeError(w, http.StatusBadRequest, "folder must be a path inside a space")
			return
		}
		cfg.Pattern = inFolder(cfg.Pattern, f)
	}
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
	if !s.clientNoteID(w, r, body.ID) {
		return
	}
	seed := "# " + day.Format("2006-01-02") + "\n\n"
	if body.Content != nil {
		seed = *body.Content
	} else if cfg.Template != "" {
		tpl := expandDaily(cfg.Template, day)
		if space != "" {
			tpl = space + "/" + tpl
		}
		if body, ok := s.templateBody(tpl); ok {
			// The same substitution every template runs, then the
			// legacy {YYYY}/{MM}/{DD}/{date} tokens the daily note
			// has always accepted.
			res := templates.Apply(string(body), templates.Values{
				Title:  day.Format("2006-01-02"),
				When:   day,
				User:   s.ident(r).Username,
				Space:  space,
				Folder: folderOfDaily(cfg.Pattern, space, day),
			})
			seed = expandDaily(res.Body, day)
		}
	}
	id := body.ID
	if id == "" {
		id = scanner.NewID(time.Now())
	}
	content, meta, _ := frontmatter.EnsureID([]byte(seed), id, time.Now())
	if meta.ID != id && body.ID != "" {
		writeError(w, http.StatusBadRequest, "the content's frontmatter names another id")
		return
	}
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
// the current editor text, which may be ahead of the file on disk; an
// optional note id says whose space embeds resolve in.
func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Markdown string `json:"markdown"`
		Note     string `json:"note"`
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
	if body.Note != "" {
		if _, ok := s.noteAuthz(w, r, body.Note); !ok {
			return
		}
		if n, err := s.DB.GetNote(r.Context(), body.Note); err == nil {
			html = s.inlineEmbeds(r.Context(), n, html)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"html": string(html)})
}

// handleNoteAppend adds text at the end of a note, on its own line, the
// way append_note does for agents; the command line's `yana today` uses
// it. It goes through the note's document, so it merges with whoever is
// typing in the note.
func (s *Server) handleNoteAppend(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.Root.Limits().MaxNoteSize+1<<10)).Decode(&body); err != nil || body.Content == "" {
		writeError(w, http.StatusBadRequest, "body must be JSON with a content field")
		return
	}
	space, role, ok := s.noteRole(w, r, id)
	if !ok {
		return
	}
	if !s.mayEdit(w, r, space, role) {
		return
	}
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "realtime editing is off on this server")
		return
	}
	n, err := s.DB.GetNote(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if n.Kind != "md" {
		writeError(w, http.StatusBadRequest, "only markdown notes take appends")
		return
	}
	author := tickAuthor
	if !s.open() {
		author = s.ident(r).Author()
	}
	cur, err := s.Sync.Text(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	next := body.Content
	if cur != "" {
		next = strings.TrimRight(cur, "\n") + "\n" + body.Content
	}
	if int64(len(next)) > s.Root.Limits().MaxNoteSize {
		writeError(w, http.StatusRequestEntityTooLarge, "the append would put the note over its size limit")
		return
	}
	if err := s.Sync.SetText(r.Context(), id, next, author); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "path": n.RelPath})
}

// editText is the body of a markdown note as a remote editor (the
// command line's `yana open`) sees it, and the document state it was read
// from. Base travels as base64 and comes back with the next save.
type editText struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Role string `json:"role,omitempty"`
	Text string `json:"text"`
	Base []byte `json:"base"`
}

// markdownForEdit is the shared front of both text handlers: the note,
// checked to be markdown, with realtime editing on.
func (s *Server) markdownForEdit(w http.ResponseWriter, r *http.Request, id string) (index.Note, bool) {
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "realtime editing is off on this server")
		return index.Note{}, false
	}
	n, err := s.DB.GetNote(r.Context(), id)
	if errors.Is(err, index.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no note with that id")
		return n, false
	}
	if err != nil {
		s.fail(w, r, err)
		return n, false
	}
	if n.Kind != "md" {
		writeError(w, http.StatusBadRequest, "only markdown notes are edited as text; html notes save through /source")
		return n, false
	}
	return n, true
}

// handleNoteText hands a remote editor the note's body and its base.
func (s *Server) handleNoteText(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, role, ok := s.noteRole(w, r, id)
	if !ok {
		return
	}
	n, ok := s.markdownForEdit(w, r, id)
	if !ok {
		return
	}
	text, base, err := s.Sync.Snapshot(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, editText{ID: id, Path: n.RelPath, Role: role, Text: text, Base: base})
}

// handleNoteTextPut saves a remote editor's text. The change is taken
// from the base the editor started from, not from the note as it is now,
// so it merges with whatever happened on the server in between instead
// of reverting it. The answer carries the merged text and the base for
// the editor's next save.
func (s *Server) handleNoteTextPut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	space, role, ok := s.noteRole(w, r, id)
	if !ok {
		return
	}
	if !s.mayEdit(w, r, space, role) {
		return
	}
	n, ok := s.markdownForEdit(w, r, id)
	if !ok {
		return
	}
	// The base is a whole document state, history included, so it can
	// outgrow the text; four times the note limit leaves it room.
	limit := 4*s.Root.Limits().MaxNoteSize + 1<<20
	var body struct {
		Base []byte  `json:"base"`
		Text *string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(&body); err != nil || body.Text == nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with base and text fields")
		return
	}
	if int64(len(*body.Text)) > s.Root.Limits().MaxNoteSize {
		writeError(w, http.StatusRequestEntityTooLarge, "content is over the note size limit")
		return
	}
	// Without accounts there is no name to put on the edit.
	author := "user:cli"
	if !s.open() {
		author = s.ident(r).Author()
	}
	text, base, err := s.Sync.EditFrom(r.Context(), id, body.Base, *body.Text, author)
	if errors.Is(err, reconcile.ErrBadBase) {
		writeError(w, http.StatusBadRequest, "base is not a state of this note; fetch the note again")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, editText{ID: id, Path: n.RelPath, Text: text, Base: base})
}
