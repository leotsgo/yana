// The tasks endpoints: a listing of every `- [ ]` line across the
// caller's spaces straight from the index, and a tick that flips one
// box through the same CRDT write the in-note checkbox uses.
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
)

// tickAuthor tags the CRDT edit a tick makes when the server runs
// without accounts, the way a history restore uses user:restore.
const tickAuthor = "user:tasks"

// taskDoneWindow is how far back the "show done" listing reaches.
const taskDoneWindow = 30 * 24 * time.Hour

// handleTasksGet answers the tasks page: open tasks by default,
// completed ones for the last 30 days with done=true. Filters: space,
// tag, path (a folder inside a space). With count=1 it answers only the
// open count, which the home screen and the sidebar badges show.
func (s *Server) handleTasksGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	space, ok := s.spaceParam(w, r)
	if !ok {
		return
	}
	var allowed []string
	if !s.open() {
		if space != "" {
			if _, ok := s.spaceAuthz(w, r, space); !ok {
				return
			}
			allowed = []string{space}
		} else {
			member, isAll, err := s.Auth.MemberSpaces(r.Context(), s.ident(r))
			if err != nil {
				s.fail(w, r, err)
				return
			}
			if !isAll {
				allowed = member
				if len(allowed) == 0 {
					allowed = []string{""} // matches nothing
				}
			}
		}
	}
	if q.Get("count") != "" {
		n, err := s.DB.OpenTaskCount(r.Context(), allowed)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"count": n})
		return
	}
	done := q.Get("done") == "true"
	tag := strings.ToLower(strings.TrimSpace(q.Get("tag")))
	path := strings.TrimSpace(q.Get("path"))
	if path != "" {
		clean, err := s.Root.Clean(path)
		if err != nil {
			writeError(w, http.StatusBadRequest, "path must be a folder inside a space")
			return
		}
		path = clean
	}
	f := index.TaskFilter{
		Space:     space,
		Allowed:   allowed,
		Done:      done,
		DoneSince: time.Now().Add(-taskDoneWindow),
		Tag:       tag,
		Path:      path,
	}
	tasks, err := s.DB.Tasks(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if tasks == nil {
		tasks = []index.Task{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

var taskBox = regexp.MustCompile(`\[([ xX])\]`)

// handleTaskTick flips one checkbox by note id and line: the document's
// line is read through the reconciliation loop, the box's character is
// flipped as the minimal edit, and the update is recorded under the
// caller's author — so a concurrent editor sees the tick live and the
// history and git blame attribute it.
func (s *Server) handleTaskTick(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Note string `json:"note"`
		Line int    `json:"line"`
		Done bool   `json:"done"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with note, line and done fields")
		return
	}
	if !validID(body.Note) {
		writeError(w, http.StatusBadRequest, "note id must be a 26-character ULID")
		return
	}
	if body.Line < 0 || body.Line > 10_000_000 {
		writeError(w, http.StatusBadRequest, "line is out of range")
		return
	}
	space, role, ok := s.noteRole(w, r, body.Note)
	if !ok {
		return
	}
	if role == "viewer" || !s.mayWrite(w, r, space) {
		return
	}
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "realtime editing is off on this server")
		return
	}
	author := tickAuthor
	if !s.open() {
		author = s.ident(r).Author()
	}
	text, err := s.Sync.Text(r.Context(), body.Note)
	if err != nil {
		if errors.Is(err, index.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no note with that id")
			return
		}
		s.fail(w, r, err)
		return
	}
	next, flip, ok := flipTaskLine(text, body.Line, body.Done)
	if !ok {
		writeError(w, http.StatusConflict, "that line is not an unticked box anymore; the list catches up on its own")
		return
	}
	if flip {
		if err := s.Sync.SetText(r.Context(), body.Note, next, author); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "done": body.Done})
}

// flipTaskLine returns the text with the box on line (0-based from the
// start of the body) set to done. ok is false when the line does not
// exist or holds no checkbox; flip is false when the box already sits
// in the asked-for state, so the caller can skip the write. The document
// holds the note body without its frontmatter, which is the line
// numbering the rendered checkbox's data-line carries too. A tick writes
// lowercase x and an untick a space, the way the in-note tick does.
func flipTaskLine(text string, line int, done bool) (next string, flip, ok bool) {
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return "", false, false
	}
	orig := lines[line]
	m := taskBox.FindStringSubmatchIndex(orig)
	if m == nil {
		return "", false, false
	}
	if (orig[m[2]:m[3]] != " ") == done {
		return text, false, true
	}
	replace := " "
	if done {
		replace = "x"
	}
	lines[line] = orig[:m[2]] + replace + orig[m[3]:]
	return strings.Join(lines, "\n"), true, true
}
