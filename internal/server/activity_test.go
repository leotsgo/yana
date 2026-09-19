package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/auth"
	"github.com/madeofpendletonwool/yana/internal/git"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

func TestSpaceActivityWithoutLayer(t *testing.T) {
	e := newEnv(t)
	if code := e.get(t, "/api/spaces/home/activity", nil); code != 501 {
		t.Fatalf("activity without git layer: %d", code)
	}
}

func TestSpaceActivityFeed(t *testing.T) {
	e := newGitEnv(t)
	ctx := context.Background()

	// Baseline: the first snapshot takes the seeded note under the
	// human identity.
	if _, err := e.gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	note, err := e.db.GetNoteByPath(ctx, "home/hist.md")
	if err != nil {
		t.Fatal(err)
	}

	// The agent edits twice, in two windows: two commits that the feed
	// must fold into one entry.
	if err := e.rec.SetText(ctx, note.ID, "agent one\n", "agent:claude"); err != nil {
		t.Fatal(err)
	}
	e.eventually(t, func() bool { return e.fileBody() == "agent one\n" })
	if _, err := e.gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.rec.SetText(ctx, note.ID, "agent two\n", "agent:claude"); err != nil {
		t.Fatal(err)
	}
	e.eventually(t, func() bool { return e.fileBody() == "agent two\n" })
	if _, err := e.gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}

	// A file that arrives outside the app commits under the filesystem
	// identity, then goes away again.
	fs := filepath.Join(e.dir, "home", "fs.md")
	if err := os.WriteFile(fs, []byte("from the files\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.gl.Author("home/fs.md", git.FilesystemAuthor)
	if _, err := e.gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fs); err != nil {
		t.Fatal(err)
	}
	if _, err := e.gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	// Let the index see both, so the feed's note resolution settles.
	e.eventually(t, func() bool {
		_, err := e.db.GetNoteByPath(ctx, "home/fs.md")
		return errors.Is(err, index.ErrNotFound)
	})

	var feed struct {
		Entries    []activityEntryJSON `json:"entries"`
		NextCursor string              `json:"next_cursor"`
		More       bool                `json:"more"`
	}
	if code := e.get(t, "/api/spaces/home/activity", &feed); code != 200 {
		t.Fatalf("feed: %d", code)
	}
	if len(feed.Entries) != 4 {
		for i, en := range feed.Entries {
			t.Logf("entry %d: %s/%s %d commits %v", i, en.Author, en.Kind, en.Commits, en.Changes)
		}
		t.Fatalf("got %d entries, want 4", len(feed.Entries))
	}

	// Newest first: the delete (person), the filesystem arrival, the
	// folded agent run, the baseline add.
	del, fsAdd, run, base := feed.Entries[0], feed.Entries[1], feed.Entries[2], feed.Entries[3]
	if del.Kind != "person" || len(del.Changes) != 1 || del.Changes[0].Action != "deleted" || del.Changes[0].Path != "home/fs.md" {
		t.Fatalf("delete entry: %+v", del)
	}
	if del.Changes[0].ID != "" {
		t.Fatal("deleted change carries a note id")
	}
	if fsAdd.Kind != "filesystem" || fsAdd.Author != "filesystem" || fsAdd.Changes[0].Action != "added" {
		t.Fatalf("filesystem entry: %+v", fsAdd)
	}
	if run.Author != "claude" || run.Kind != "agent" || run.Commits != 2 {
		t.Fatalf("agent run entry: %+v", run)
	}
	if run.From.After(run.To) {
		t.Fatalf("agent run times: %v after %v", run.From, run.To)
	}
	if len(run.Changes) != 1 || run.Changes[0].Action != "modified" || run.Changes[0].Path != "home/hist.md" || run.Changes[0].ID != note.ID {
		t.Fatalf("agent run changes: %+v", run.Changes)
	}
	if base.Kind != "person" || base.Commits != 1 || base.Changes[0].Action != "added" || base.Changes[0].ID != note.ID {
		t.Fatalf("baseline entry: %+v", base)
	}

	// The cursor pages without repeating anything.
	var page1, page2 struct {
		Entries    []activityEntryJSON `json:"entries"`
		NextCursor string              `json:"next_cursor"`
		More       bool                `json:"more"`
	}
	if code := e.get(t, "/api/spaces/home/activity?limit=3", &page1); code != 200 || len(page1.Entries) != 3 || !page1.More || page1.NextCursor == "" {
		t.Fatalf("page 1: %d %+v", code, page1)
	}
	if code := e.get(t, "/api/spaces/home/activity?limit=3&cursor="+page1.NextCursor, &page2); code != 200 || len(page2.Entries) != 1 {
		t.Fatalf("page 2: %d %+v", code, page2)
	}
	if page2.More || page2.NextCursor != "" {
		t.Fatalf("history exhausted but the page offers more: %+v", page2)
	}
	seen := map[string]bool{}
	for _, en := range append(append([]activityEntryJSON{}, page1.Entries...), page2.Entries...) {
		key := en.Author + "|" + en.From.Format(time.RFC3339Nano) + "|" + en.To.Format(time.RFC3339Nano)
		for _, c := range en.Changes {
			key += "|" + c.Action + " " + c.Path
		}
		if seen[key] {
			t.Fatalf("pages repeat an entry: %s", key)
		}
		seen[key] = true
	}
	if len(seen) != 4 {
		t.Fatalf("pages cover %d unique entries, want 4", len(seen))
	}

	// One author only.
	var only struct {
		Entries []activityEntryJSON `json:"entries"`
	}
	if code := e.get(t, "/api/spaces/home/activity?author=claude", &only); code != 200 || len(only.Entries) != 1 || only.Entries[0].Author != "claude" {
		t.Fatalf("author filter: %d %+v", code, only.Entries)
	}

	// A subtree with nothing in it, a bad cursor, and a window that has
	// not opened yet.
	if code := e.get(t, "/api/spaces/home/activity?path=sub", &only); code != 200 || len(only.Entries) != 0 {
		t.Fatalf("subtree filter: %d %d entries", code, len(only.Entries))
	}
	if code := e.get(t, "/api/spaces/home/activity?cursor=zzzz", nil); code != 400 {
		t.Fatalf("bad cursor: %d", code)
	}
	if code := e.get(t, "/api/spaces/home/activity?since="+time.Now().Add(time.Hour).UTC().Format(time.RFC3339), &only); code != 200 || len(only.Entries) != 0 {
		t.Fatalf("future since: %d %d entries", code, len(only.Entries))
	}
}

// TestSpaceActivityMembership: the feed answers only inside the
// identity's spaces, like every other space endpoint.
func TestSpaceActivityMembership(t *testing.T) {
	dir := t.TempDir()
	writeYAML := func(rel, body string) {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(abs), 0o755)
		os.WriteFile(abs, []byte(body), 0o644)
	}
	writeYAML("home/mine.md", "# Mine\n")
	writeYAML("theirs/secret.md", "# Secret\n")
	writeYAML("home/.space.yml", "name: home\nmembers:\n  - user: member\n    role: editor\n")

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
	// The accounts exist before the scan so the space's member list can
	// resolve its usernames.
	ctx := context.Background()
	if _, _, err := as.Setup(ctx, "owner", "owner-password1", "t"); err != nil {
		t.Fatal(err)
	}
	_, tok, err := as.Login(ctx, "owner", "owner-password1", "t")
	if err != nil {
		t.Fatal(err)
	}
	ownerID, err := as.VerifyAccess(tok.Access)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := as.CreateUser(ctx, ownerID, "member", "member-password1"); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	gl := git.New(dir, git.Options{Quiet: time.Hour, Interval: time.Hour, SecretPath: filepath.Join(dir, ".sync", "git_secret")}, nil)
	if err := gl.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gl.Close)
	srv := New(Deps{DB: db, Root: root, Auth: as, Git: gl, Version: "test"})
	srv.SetReady(true)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	header := func(user, pass string) string {
		_, tok, err := as.Login(ctx, user, pass, "t")
		if err != nil {
			t.Fatal(err)
		}
		return "Bearer " + tok.Access
	}
	member := header("member", "member-password1")
	owner := header("owner", "owner-password1")

	if code, _ := doGet(t, ts, "/api/spaces/home/activity", member); code != 200 {
		t.Fatalf("member reading own space's activity: %d", code)
	}
	if code, _ := doGet(t, ts, "/api/spaces/theirs/activity", member); code != 404 {
		t.Fatalf("member reading another space's activity: %d", code)
	}
	if code, _ := doGet(t, ts, "/api/spaces/home/activity", owner); code != 200 {
		t.Fatalf("owner reading activity: %d", code)
	}
	if code, _ := doGet(t, ts, "/api/spaces/home/activity", ""); code != 401 {
		t.Fatalf("anonymous reading activity: %d", code)
	}
}

func TestChangeFolderFoldsAPathHistory(t *testing.T) {
	ch := func(cs ...git.ActivityChange) []git.ActivityChange { return cs }
	f := newChangeFolder()
	f.apply(ch(git.ActivityChange{Status: "A", Path: "a.md"}))
	f.apply(ch(git.ActivityChange{Status: "M", Path: "a.md"}))
	f.apply(ch(git.ActivityChange{Status: "R", Path: "b.md", Orig: "a.md"})) // renamed after being added: still added
	if got := f.changes(); len(got) != 1 || got[0].Action != "added" || got[0].Path != "b.md" || got[0].From != "" {
		t.Fatalf("added then renamed: %+v", got)
	}

	f.reset()
	f.apply(ch(git.ActivityChange{Status: "A", Path: "x.md"}))
	f.apply(ch(git.ActivityChange{Status: "D", Path: "x.md"})) // added then deleted inside one run: nothing happened
	if got := f.changes(); len(got) != 0 {
		t.Fatalf("added then deleted: %+v", got)
	}

	f.reset()
	f.apply(ch(git.ActivityChange{Status: "R", Path: "mid.md", Orig: "old.md"}))
	f.apply(ch(git.ActivityChange{Status: "R", Path: "new.md", Orig: "mid.md"})) // a rename chain keeps the oldest path
	if got := f.changes(); len(got) != 1 || got[0].Action != "renamed" || got[0].Path != "new.md" || got[0].From != "old.md" {
		t.Fatalf("rename chain: %+v", got)
	}

	f.reset()
	f.apply(ch(git.ActivityChange{Status: "D", Path: "gone.md"}))
	if got := f.changes(); len(got) != 1 || got[0].Action != "deleted" || got[0].Path != "gone.md" {
		t.Fatalf("plain delete: %+v", got)
	}

	f.reset()
	f.apply(ch(git.ActivityChange{Status: "C", Path: "copy.md", Orig: "orig.md"})) // a copy is a new note
	if got := f.changes(); len(got) != 1 || got[0].Action != "added" || got[0].Path != "copy.md" {
		t.Fatalf("copy: %+v", got)
	}
}
