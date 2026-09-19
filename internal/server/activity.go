// Space activity endpoint: the git history of one space (or a subtree
// of it) rendered as a feed — who changed what, when. A run of commits
// by the same agent inside a quiet stretch reads as one entry, so an
// overnight agent session shows once; a person's commits read one per
// quiet window, which is how they were made.
package server

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/git"
)

// agentRunGap is how far apart two commits by the same agent can fall
// and still belong to one feed entry. An agent that writes steadily
// through a night stays one entry; a new run the next morning is a new
// one.
const agentRunGap = 2 * time.Hour

// activityBatch is how many raw commits one git walk returns; the feed
// fetches batches until a page of entries is full or the history ends.
const activityBatch = 500

// activityWalkCap bounds the commits one request will walk, so a filter
// that matches nothing on a long history answers in bounded time.
const activityWalkCap = 4000

// activityChangeJSON is one note as a feed entry touched it. The id and
// title are present when the note still exists — deleted notes carry
// their path, renamed ones the path they hold now.
type activityChangeJSON struct {
	Action string `json:"action"` // added, modified, renamed, deleted
	Path   string `json:"path"`
	From   string `json:"from,omitempty"` // previous path, renames only
	ID     string `json:"id,omitempty"`
	Title  string `json:"title,omitempty"`
}

type activityEntryJSON struct {
	Author  string               `json:"author"`
	Kind    string               `json:"kind"` // person, agent, filesystem
	From    time.Time            `json:"from"` // oldest commit in the entry
	To      time.Time            `json:"to"`   // newest commit in the entry
	Commits int                  `json:"commits"`
	Changes []activityChangeJSON `json:"changes"`
}

// handleSpaceActivity lists a space's history as the activity feed.
func (s *Server) handleSpaceActivity(w http.ResponseWriter, r *http.Request) {
	if s.gitUnavailable(w) {
		return
	}
	space, ok := s.spacePathParam(w, r)
	if !ok {
		return
	}
	if _, ok := s.spaceAuthz(w, r, space); !ok {
		return
	}
	q := r.URL.Query()

	// The optional subtree: a folder inside the space, relative to it.
	scope := space
	if sub := q.Get("path"); sub != "" {
		clean, err := s.Root.Clean(sub)
		if err != nil || clean == "" {
			writeError(w, http.StatusBadRequest, "path must be a folder inside the space")
			return
		}
		scope = space + "/" + clean
	}

	var since, until time.Time
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be an RFC3339 timestamp")
			return
		}
		since = t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "until must be an RFC3339 timestamp")
			return
		}
		until = t
	}
	author := q.Get("author")
	if len(author) > 100 {
		writeError(w, http.StatusBadRequest, "author is longer than 100 characters")
		return
	}
	limit := 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, "limit must be a number from 1 to 200")
			return
		}
		limit = n
	}
	cursor := q.Get("cursor")
	if cursor != "" && !git.ValidRevision(cursor) {
		writeError(w, http.StatusBadRequest, "cursor must be a commit hash from the feed")
		return
	}

	entries, next, more, err := s.activityFeed(r, space, scope, since, until, author, cursor, limit)
	if err != nil {
		if err == git.ErrNoSuchCommit {
			writeError(w, http.StatusBadRequest, "cursor is not from this history")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries, "next_cursor": next, "more": more,
	})
}

// spacePathParam validates the {space} path parameter the way spaceParam
// validates the query one: a single clean directory name.
func (s *Server) spacePathParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	space := r.PathValue("space")
	clean, err := s.Root.Clean(space)
	if space == "" || err != nil || clean == "" || strings.Contains(clean, "/") {
		writeError(w, http.StatusBadRequest, "space must be a single directory name")
		return "", false
	}
	return clean, true
}

// activityFeed walks the scope's commits and folds them into feed
// entries. It returns the entries, the cursor for the next page, and
// whether older commits remain. The cursor is the oldest commit the
// page consumed — skipped by a filter or folded into an entry alike —
// so the next page starts strictly below it.
func (s *Server) activityFeed(r *http.Request, space, scope string, since, until time.Time, author, cursor string, limit int) ([]activityEntryJSON, string, bool, error) {
	before := cursor
	walked := 0
	entries := []activityEntryJSON{}
	var run []git.ActivityCommit // the open agent run, newest commit first
	merge := newChangeFolder()

	closeRun := func() {
		if len(run) == 0 {
			return
		}
		e := activityEntryJSON{
			Author:  run[0].Name,
			Kind:    run[0].Kind,
			To:      run[0].Date,
			From:    run[len(run)-1].Date,
			Commits: len(run),
		}
		// Fold the run's commits oldest first, so a later action on a
		// path lands on top of the earlier one.
		for i := len(run) - 1; i >= 0; i-- {
			merge.apply(run[i].Changes)
		}
		run = run[:0]
		e.Changes = merge.changes()
		merge.reset()
		entries = append(entries, e)
	}

	pageFull := false
	for !pageFull && len(entries) < limit && walked < activityWalkCap {
		commits, err := s.Git.ActivityLog(r.Context(), scope, since, until, before, activityBatch)
		if err != nil {
			return nil, "", false, err
		}
		walked += len(commits)
		for _, c := range commits {
			if author != "" && c.Name != author {
				before = c.Hash
				continue
			}
			continues := c.Kind == "agent" && len(run) > 0 &&
				run[0].Name == c.Name && run[0].Date.Sub(c.Date) <= agentRunGap
			if !continues {
				closeRun()
				if len(entries) >= limit {
					// The entry that just closed is the page's last;
					// this commit opens the next page's history, so the
					// cursor stays above it.
					pageFull = true
					break
				}
			}
			run = append(run, c)
			before = c.Hash
		}
		if pageFull {
			break
		}
		if len(commits) < activityBatch {
			// History exhausted: land the open run and finish.
			closeRun()
			return s.resolveNotes(r, space, entries), "", false, nil
		}
	}
	if !pageFull {
		// The walk cap, not the history, stopped the page.
		closeRun()
	}
	return s.resolveNotes(r, space, entries), before, true, nil
}

// changeFolder folds a run of commits' changes into one action per
// final path: an added note that is edited stays added, a note added
// and then deleted disappears, a rename chain keeps its oldest path.
type changeFolder struct {
	acc map[string]activityChangeJSON
}

func newChangeFolder() *changeFolder {
	return &changeFolder{acc: map[string]activityChangeJSON{}}
}

func (f *changeFolder) reset() {
	f.acc = map[string]activityChangeJSON{}
}

// apply folds one commit's changes; commits arrive oldest first.
func (f *changeFolder) apply(cs []git.ActivityChange) {
	for _, c := range cs {
		switch c.Status {
		case "A":
			f.acc[c.Path] = activityChangeJSON{Action: "added", Path: c.Path}
		case "D":
			if prev, ok := f.acc[c.Path]; ok && prev.Action == "added" {
				delete(f.acc, c.Path)
				continue
			}
			f.acc[c.Path] = activityChangeJSON{Action: "deleted", Path: c.Path}
		case "R", "C":
			prev, had := f.acc[c.Orig]
			delete(f.acc, c.Orig)
			if !had || prev.Action == "deleted" {
				// Nothing lived at the old path, or it had already
				// gone. A rename (re)creates the note where it is now;
				// a copy is a new note and reads as added.
				action, from := "renamed", c.Orig
				if c.Status == "C" || prev.Action == "deleted" {
					action, from = "added", ""
				}
				f.acc[c.Path] = activityChangeJSON{Action: action, Path: c.Path, From: from}
				continue
			}
			// The note keeps the action it already earned at its old
			// path; a renamed note that was added reads as added.
			prev.Path = c.Path
			f.acc[c.Path] = prev
		default: // M, and anything else a diff can say
			if _, ok := f.acc[c.Path]; !ok {
				f.acc[c.Path] = activityChangeJSON{Action: "modified", Path: c.Path}
			}
		}
	}
}

// changes returns the folded actions, ordered by path.
func (f *changeFolder) changes() []activityChangeJSON {
	out := make([]activityChangeJSON, 0, len(f.acc))
	for _, c := range f.acc {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// resolveNotes stamps each change with the note's id and title when the
// note still exists at that path, in one index read.
func (s *Server) resolveNotes(r *http.Request, space string, entries []activityEntryJSON) []activityEntryJSON {
	notes, err := s.DB.ListNotes(r.Context(), space)
	if err != nil {
		return entries // paths only; the feed is still true
	}
	byPath := make(map[string]noteRef, len(notes))
	for _, n := range notes {
		byPath[n.RelPath] = noteRef{n.ID, n.Title}
	}
	for i := range entries {
		for j := range entries[i].Changes {
			// A deleted note carries its path only: the index can lag
			// the git truth, and a link that opens nowhere is worse
			// than none.
			c := &entries[i].Changes[j]
			if c.Action == "deleted" {
				continue
			}
			if n, ok := byPath[c.Path]; ok {
				c.ID = n.id
				c.Title = n.title
			}
		}
	}
	return entries
}

type noteRef struct{ id, title string }
