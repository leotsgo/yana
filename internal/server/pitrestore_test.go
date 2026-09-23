package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/auth"
	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/git"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// createNote makes a note through the API and returns its id.
func (e *gitEnv) createNote(t *testing.T, path, body string) string {
	t.Helper()
	var res struct{ ID string }
	if code := e.post(t, "/api/notes", map[string]string{"path": path, "content": body}, &res); code != 201 {
		t.Fatalf("create %s: %d", path, code)
	}
	if res.ID == "" {
		t.Fatal("create returned no id")
	}
	return res.ID
}

// deleteNote removes a note through the API (the soft delete).
func (e *gitEnv) deleteNote(t *testing.T, id string) int {
	t.Helper()
	req, err := http.NewRequest("DELETE", e.ts.URL+"/api/notes/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// headOf is the hash of the newest commit.
func (e *gitEnv) headOf(t *testing.T) string {
	t.Helper()
	entries, err := e.gl.Log(context.Background(), "home/hist.md", 1)
	if err != nil || len(entries) == 0 {
		t.Fatalf("head: %v %+v", err, entries)
	}
	return entries[0].Hash
}

// bodyAt reads a note file's body from the tree.
func bodyAt(dir, rel string) string {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return "<missing>"
	}
	return string(frontmatter.Parse(b).Body)
}

// runGitAt runs one git command over a tree and returns its output.
func runGitAt(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestPITRestoreTree covers the acceptance: a folder of ten notes
// deleted, the tree restored to the commit before, all ten back with
// their ids and their own histories intact — and a preview that said
// exactly what the restore then did.
func TestPITRestoreTree(t *testing.T) {
	e := newGitEnv(t)
	ctx := context.Background()

	// A folder of ten notes, committed.
	ids := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		path := "home/folder/n" + string(rune('a'+i)) + ".md"
		ids = append(ids, e.createNote(t, path, "version one\n"))
	}
	for _, id := range ids {
		e.eventually(t, func() bool {
			_, err := e.db.Body(ctx, id)
			return err == nil
		})
	}
	if _, err := e.gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	before := e.headOf(t)

	// Delete the folder through the app.
	for _, id := range ids {
		if code := e.deleteNote(t, id); code != 200 {
			t.Fatalf("delete %s: %d", id, code)
		}
	}
	e.eventually(t, func() bool {
		_, err := e.db.GetNoteByPath(ctx, "home/folder/na.md")
		return errors.Is(err, index.ErrNotFound)
	})
	if _, err := e.gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}

	// The preview says ten notes return, exactly.
	var prev struct {
		Preview struct {
			Commit  string `json:"commit"`
			Added   int    `json:"added"`
			Changed int    `json:"changed"`
			Deleted int    `json:"deleted"`
			Moved   int    `json:"moved"`
			Changes []struct {
				Action string `json:"action"`
				Path   string `json:"path"`
			} `json:"changes"`
		} `json:"preview"`
	}
	if code := e.post(t, "/api/git/restore/preview", map[string]string{"commit": before}, &prev); code != 200 {
		t.Fatalf("preview: %d", code)
	}
	if prev.Preview.Added != 10 || prev.Preview.Changed != 0 || prev.Preview.Deleted != 0 || prev.Preview.Moved != 0 {
		t.Fatalf("preview counts: %+v", prev.Preview)
	}
	if len(prev.Preview.Changes) != 10 || prev.Preview.Changes[0].Action != "added" {
		t.Fatalf("preview list: %+v", prev.Preview.Changes)
	}

	var res struct {
		Ok      bool   `json:"ok"`
		Commit  string `json:"commit"`
		Tag     string `json:"tag"`
		Added   int    `json:"added"`
		Changed int    `json:"changed"`
		Deleted int    `json:"deleted"`
		Moved   int    `json:"moved"`
	}
	if code := e.post(t, "/api/git/restore", map[string]string{"commit": before}, &res); code != 200 {
		t.Fatalf("restore: %d", code)
	}
	if res.Added != prev.Preview.Added || res.Changed != prev.Preview.Changed || res.Deleted != prev.Preview.Deleted || res.Moved != prev.Preview.Moved {
		t.Fatalf("restore did what the preview said: %+v vs %+v", res, prev.Preview)
	}
	if res.Tag == "" || res.Commit == "" {
		t.Fatalf("restore reports its landing: %+v", res)
	}

	// All ten are back with their ids, each with its own history.
	for i, id := range ids {
		path := "home/folder/n" + string(rune('a'+i)) + ".md"
		e.eventually(t, func() bool {
			body, err := e.db.Body(ctx, id)
			return err == nil && body == "version one\n"
		})
		if _, err := e.db.GetNoteByPath(ctx, path); err != nil {
			t.Fatalf("note %s not back at %s: %v", id, path, err)
		}
		entries, err := e.gl.Log(ctx, path, 10)
		if err != nil || len(entries) < 2 {
			t.Fatalf("note %s history after the restore: %v %+v", path, err, entries)
		}
	}

	// The history reads honestly: the newest commit is the restore. The
	// account-less server has no person to name, so the human identity
	// authors it.
	out := runGitAt(t, e.dir, "log", "-1", "--format=%an %s")
	if out != "yana user restore the tree to "+before[:7]+"\n" {
		t.Fatalf("restore commit: %q", out)
	}

	// The feed carries the commit each entry restores to.
	var feed struct {
		Entries []activityEntryJSON `json:"entries"`
		Restore bool                `json:"restore_allowed"`
	}
	if code := e.get(t, "/api/spaces/home/activity", &feed); code != 200 {
		t.Fatalf("feed: %d", code)
	}
	if !feed.Restore || len(feed.Entries) == 0 || feed.Entries[0].Commit == "" {
		t.Fatalf("feed carries restore points: %+v", feed.Entries)
	}

	// A bad commit is refused, not guessed at.
	if code := e.post(t, "/api/git/restore/preview", map[string]string{"commit": "ffffffffff"}, nil); code != 400 {
		t.Fatalf("unknown commit: %d", code)
	}
}

// TestPITRestoreSpaceScoped: restoring one space leaves every other
// space byte-identical, and the commit subject says which space.
func TestPITRestoreSpaceScoped(t *testing.T) {
	e := newGitEnv(t)
	ctx := context.Background()

	homeID := e.createNote(t, "home/mine.md", "mine v1\n")
	workID := e.createNote(t, "work/theirs.md", "theirs v1\n")
	e.eventually(t, func() bool { _, err := e.db.Body(ctx, homeID); return err == nil })
	e.eventually(t, func() bool { _, err := e.db.Body(ctx, workID); return err == nil })
	if _, err := e.gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	before := e.headOf(t)

	// Both spaces move on: one edit here, one edit and an addition there.
	if err := e.rec.SetText(ctx, homeID, "mine v2\n", "user:fox"); err != nil {
		t.Fatal(err)
	}
	if err := e.rec.SetText(ctx, workID, "theirs v2\n", "user:fox"); err != nil {
		t.Fatal(err)
	}
	e.eventually(t, func() bool { return bodyAt(e.dir, "home/mine.md") == "mine v2\n" })
	e.eventually(t, func() bool { return bodyAt(e.dir, "work/theirs.md") == "theirs v2\n" })
	e.createNote(t, "work/extra.md", "extra\n")
	if _, err := e.gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}

	var res struct {
		Added   int `json:"added"`
		Changed int `json:"changed"`
	}
	if code := e.post(t, "/api/git/restore", map[string]string{"commit": before, "space": "home"}, &res); code != 200 {
		t.Fatalf("space restore: %d", code)
	}
	if res.Changed != 1 || res.Added != 0 {
		t.Fatalf("space restore counts: %+v", res)
	}
	e.eventually(t, func() bool { return bodyAt(e.dir, "home/mine.md") == "mine v1\n" })
	if got := bodyAt(e.dir, "work/theirs.md"); got != "theirs v2\n" {
		t.Fatalf("the other space moved: %q", got)
	}
	if got := bodyAt(e.dir, "work/extra.md"); got != "extra\n" {
		t.Fatalf("the other space's new note moved: %q", got)
	}
	out := runGitAt(t, e.dir, "log", "-1", "--format=%s")
	if out != "restore home to "+before[:7]+"\n" {
		t.Fatalf("space restore subject: %q", out)
	}
}

// TestDeletedNotesListAndRestore: the Data page's list restores a single
// note to its original path, and to a free name when the path is taken.
func TestDeletedNotesListAndRestore(t *testing.T) {
	e := newGitEnv(t)
	ctx := context.Background()

	id := e.createNote(t, "home/lost.md", "the lost text\n")
	e.eventually(t, func() bool { _, err := e.db.Body(ctx, id); return err == nil })
	if _, err := e.gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if code := e.deleteNote(t, id); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	e.eventually(t, func() bool {
		_, err := e.db.GetNoteByPath(ctx, "home/lost.md")
		return errors.Is(err, index.ErrNotFound)
	})

	var list struct {
		Entries []struct {
			ID        string `json:"id"`
			Path      string `json:"path"`
			HasFile   bool   `json:"has_file"`
			InHistory bool   `json:"in_history"`
		} `json:"entries"`
	}
	if code := e.get(t, "/api/deleted-notes", &list); code != 200 {
		t.Fatalf("deleted notes: %d", code)
	}
	if len(list.Entries) != 1 || list.Entries[0].ID != id || !list.Entries[0].HasFile {
		t.Fatalf("deleted list: %+v", list.Entries)
	}

	var res struct {
		Ok       bool   `json:"ok"`
		Path     string `json:"path"`
		Conflict bool   `json:"conflict"`
		From     string `json:"from"`
	}
	if code := e.post(t, "/api/deleted-notes/"+id+"/restore", map[string]string{}, &res); code != 200 {
		t.Fatalf("restore deleted: %d", code)
	}
	if res.Path != "home/lost.md" || res.Conflict || res.From != "trash" {
		t.Fatalf("restore deleted result: %+v", res)
	}
	e.eventually(t, func() bool {
		body, err := e.db.Body(ctx, id)
		return err == nil && body == "the lost text\n"
	})

	// Delete it again, take the path with a new note, and watch the
	// restore land on a free name.
	if code := e.deleteNote(t, id); code != 200 {
		t.Fatalf("delete again: %d", code)
	}
	e.eventually(t, func() bool {
		_, err := e.db.GetNoteByPath(ctx, "home/lost.md")
		return errors.Is(err, index.ErrNotFound)
	})
	occupant := e.createNote(t, "home/lost.md", "someone new\n")
	e.eventually(t, func() bool { _, err := e.db.Body(ctx, occupant); return err == nil })
	res = struct {
		Ok       bool   `json:"ok"`
		Path     string `json:"path"`
		Conflict bool   `json:"conflict"`
		From     string `json:"from"`
	}{}
	if code := e.post(t, "/api/deleted-notes/"+id+"/restore", map[string]string{}, &res); code != 200 {
		t.Fatalf("restore into an occupied path: %d", code)
	}
	if !res.Conflict || res.Path == "home/lost.md" {
		t.Fatalf("conflict restore landed on %q", res.Path)
	}
	e.eventually(t, func() bool {
		body, err := e.db.Body(ctx, id)
		return err == nil && body == "the lost text\n"
	})
	if got := bodyAt(e.dir, "home/lost.md"); got != "someone new\n" {
		t.Fatalf("the occupant was clobbered: %q", got)
	}
}

// TestDeletedNoteRestoresFromHistory: with neither the trash copy nor
// the retained document left, the note's last committed revision is
// the way back.
func TestDeletedNoteRestoresFromHistory(t *testing.T) {
	e := newGitEnv(t)
	ctx := context.Background()

	id := e.createNote(t, "home/ancient.md", "committed words\n")
	e.eventually(t, func() bool { _, err := e.db.Body(ctx, id); return err == nil })
	if _, err := e.gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if code := e.deleteNote(t, id); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	e.eventually(t, func() bool {
		_, err := e.db.GetNoteByPath(ctx, "home/ancient.md")
		return errors.Is(err, index.ErrNotFound)
	})

	// The trash copy and the retained document both go away; the
	// history is what remains.
	row, err := e.db.GetDeleted(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(e.dir, filepath.FromSlash(row.TrashPath)),
		filepath.Join(e.dir, ".sync", "crdt", "retired", id+".bin"),
		filepath.Join(e.dir, ".sync", "crdt", id+".bin"),
	} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove %s: %v", p, err)
		}
	}

	var list struct {
		Entries []struct {
			ID        string `json:"id"`
			InHistory bool   `json:"in_history"`
		} `json:"entries"`
	}
	if code := e.get(t, "/api/deleted-notes", &list); code != 200 || len(list.Entries) != 1 || !list.Entries[0].InHistory {
		t.Fatalf("history-only list: %d %+v", code, list.Entries)
	}

	var res struct {
		Path string `json:"path"`
		From string `json:"from"`
	}
	if code := e.post(t, "/api/deleted-notes/"+id+"/restore", map[string]string{}, &res); code != 200 {
		t.Fatalf("restore from history: %d", code)
	}
	if res.Path != "home/ancient.md" || res.From != "history" {
		t.Fatalf("history restore: %+v", res)
	}
	e.eventually(t, func() bool {
		body, err := e.db.Body(ctx, id)
		return err == nil && body == "committed words\n"
	})
}

// newTestReconciler is the loop wired the way main wires it, for test
// environments the gitEnv harness does not cover.
func newTestReconciler(root *pathsafe.Root, db *index.DB, sc *scanner.Scanner, gl *git.Layer) *reconcile.Reconciler {
	rec := reconcile.New(root, db, sc, reconcile.Options{
		IdleTime: 60 * time.Millisecond, Debounce: 20 * time.Millisecond,
		SettleTime: 150 * time.Millisecond, UnloadAfter: -1, OnTreeChange: gl.Notify,
	}, nil)
	if err := rec.Start(); err != nil {
		panic(err)
	}
	gl.Attach(rec)
	return rec
}

// TestPITRestorePermissions: a tree restore is owner only, a space
// restore needs write access on that space, and an agent token — which
// has no place on /api at all — is refused before any of it.
func TestPITRestorePermissions(t *testing.T) {
	dir := t.TempDir()
	writeYAML := func(rel, body string) {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(abs), 0o755)
		os.WriteFile(abs, []byte(body), 0o644)
	}
	writeYAML("home/mine.md", "# Mine\n")
	writeYAML("home/.space.yml", "name: home\nmembers:\n  - user: viewer\n    role: viewer\n  - user: editor\n    role: editor\n")

	root, err := pathsafe.NewRoot(dir, pathsafe.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(filepath.Join(dir, ".sync", "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sc := scanner.New(root, db, scanner.Options{SettleTime: time.Millisecond}, nil)
	as, err := auth.Open(db, filepath.Join(dir, ".sync", "auth_secret"), auth.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, _, err := as.Setup(ctx, "owner", "owner-password1", "t"); err != nil {
		t.Fatal(err)
	}
	login := func(user, pass string) string {
		t.Helper()
		_, tok, err := as.Login(ctx, user, pass, "t")
		if err != nil {
			t.Fatal(err)
		}
		return tok.Access
	}
	ownerAccess := login("owner", "owner-password1")
	ownerID, err := as.VerifyAccess(ownerAccess)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"viewer", "editor"} {
		if _, err := as.CreateUser(ctx, ownerID, u, u+"-password1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sc.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	gl := git.New(dir, git.Options{Quiet: time.Hour, Interval: time.Hour, DB: db, SecretPath: filepath.Join(dir, ".sync", "git_secret")}, nil)
	if err := gl.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	rec := newTestReconciler(root, db, sc, gl)
	t.Cleanup(func() {
		rec.Close()
		gl.Close()
	})
	if _, err := gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	entries, err := gl.Log(ctx, "home/mine.md", 1)
	if err != nil || len(entries) == 0 {
		t.Fatalf("log: %v", err)
	}
	commit := entries[0].Hash

	srv := New(Deps{DB: db, Root: root, Auth: as, Git: gl, Sync: rec, Scanner: sc, Version: "test"})
	srv.SetReady(true)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	post := func(path, token string, body map[string]string) int {
		t.Helper()
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest("POST", ts.URL+path, &buf)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	viewer := login("viewer", "viewer-password1")
	editor := login("editor", "editor-password1")
	owner := login("owner", "owner-password1")
	agentTok, err := as.CreateAgentToken(ctx, ownerID, "helper", []string{"home"}, true)
	if err != nil {
		t.Fatal(err)
	}

	// Agent tokens never restore: they have no account session.
	if code := post("/api/git/restore", agentTok.Secret, map[string]string{"commit": commit}); code != 401 {
		t.Fatalf("agent token on the restore: %d", code)
	}
	// A viewer of the space cannot restore it, or even preview it.
	if code := post("/api/git/restore", viewer, map[string]string{"commit": commit, "space": "home"}); code != 403 {
		t.Fatalf("viewer restoring a space: %d", code)
	}
	if code := post("/api/git/restore/preview", viewer, map[string]string{"commit": commit, "space": "home"}); code != 403 {
		t.Fatalf("viewer previewing a space restore: %d", code)
	}
	// A non-owner cannot restore the whole tree.
	if code := post("/api/git/restore", editor, map[string]string{"commit": commit}); code != 403 {
		t.Fatalf("editor restoring the tree: %d", code)
	}
	// An editor of the space can, and the commit is theirs. Something
	// has to have moved since the commit for the restore to do.
	if n, err := db.GetNoteByPath(ctx, "home/mine.md"); err != nil {
		t.Fatal(err)
	} else if err := rec.SetText(ctx, n.ID, "# Mine\n\nmoved on\n", "user:editor"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && bodyAt(dir, "home/mine.md") != "# Mine\n\nmoved on\n" {
		time.Sleep(15 * time.Millisecond)
	}
	if bodyAt(dir, "home/mine.md") != "# Mine\n\nmoved on\n" {
		t.Fatalf("the edit never landed: %q", bodyAt(dir, "home/mine.md"))
	}
	if _, err := gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if code := post("/api/git/restore", editor, map[string]string{"commit": commit, "space": "home"}); code != 200 {
		t.Fatalf("editor restoring a space: %d", code)
	}
	out := runGitAt(t, dir, "log", "-3", "--format=%an <%ae> %s")
	if !strings.Contains(out, "editor <editor@yana.local> restore home to "+commit[:7]) {
		t.Fatalf("the editor's restore commit not found:\n%s", out)
	}
	if got := bodyAt(dir, "home/mine.md"); got != "# Mine\n" {
		t.Fatalf("the space did not return to the commit: %q", got)
	}
	// The owner can restore the tree to the same state (a no-op that
	// changes nothing and commits nothing new).
	if code := post("/api/git/restore", owner, map[string]string{"commit": commit}); code != 200 {
		t.Fatalf("owner restoring the tree: %d", code)
	}
}
