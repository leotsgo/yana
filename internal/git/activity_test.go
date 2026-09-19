package git_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/git"
)

func TestActivityLogListsStatusesKindsAndRenames(t *testing.T) {
	l := newLayer(t, testOptions())
	ctx := context.Background()

	// A human baseline, an agent edit, a filesystem edit, a rename, and
	// a delete, each in its own snapshot so the log holds one commit
	// per author per window.
	write(t, l, "home/one.md", "one\n")
	write(t, l, "home/two.md", "two\n")
	l.Author("home/one.md", "agent:claude")
	l.Author("home/two.md", "agent:claude")
	if _, err := l.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}

	write(t, l, "home/one.md", "one edited\n")
	l.Author("home/one.md", "agent:claude")
	if _, err := l.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}

	write(t, l, "home/two.md", "two from the files\n")
	l.Author("home/two.md", git.FilesystemAuthor)
	if _, err := l.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}

	renameAbs := l.Root() + "/home/one.md"
	if err := os.Rename(renameAbs, l.Root()+"/home/one-moved.md"); err != nil {
		t.Fatal(err)
	}
	l.Author("home/one.md", "user:fox")
	l.Author("home/one-moved.md", "user:fox")
	if _, err := l.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(l.Root() + "/home/two.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}

	commits, err := l.ActivityLog(ctx, "home", time.Time{}, time.Time{}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Newest first: delete (human), rename (human), filesystem edit,
	// agent edit, agent add. The .gitignore commit is outside the scope
	// and never appears.
	if len(commits) != 5 {
		for i, c := range commits {
			t.Logf("commit %d: %s %s %v", i, c.Hash[:8], c.Name, c.Changes)
		}
		t.Fatalf("got %d commits, want 5", len(commits))
	}
	want := []struct {
		name string
		kind string
	}{
		{"yana user", "person"},
		{"yana user", "person"},
		{"filesystem", "filesystem"},
		{"claude", "agent"},
		{"claude", "agent"},
	}
	for i, w := range want {
		if commits[i].Name != w.name || commits[i].Kind != w.kind {
			t.Fatalf("commit %d author = %s/%s, want %s/%s", i, commits[i].Name, commits[i].Kind, w.name, w.kind)
		}
	}
	// The newest commit is the delete.
	if len(commits[0].Changes) != 1 || commits[0].Changes[0].Status != "D" || commits[0].Changes[0].Path != "home/two.md" {
		t.Fatalf("delete commit changes: %+v", commits[0].Changes)
	}
	// The rename carries both halves.
	rn := commits[1].Changes
	if len(rn) != 1 || rn[0].Status != "R" || rn[0].Path != "home/one-moved.md" || rn[0].Orig != "home/one.md" {
		t.Fatalf("rename commit changes: %+v", rn)
	}
	// A subtree scope narrows the walk.
	sub, err := l.ActivityLog(ctx, "home/nothere", time.Time{}, time.Time{}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 0 {
		t.Fatalf("empty scope returned %d commits", len(sub))
	}
}

func TestFilesystemWindowCommitsUnderItsOwnIdentity(t *testing.T) {
	l := newLayer(t, testOptions())
	ctx := context.Background()
	write(t, l, "home/fs.md", "from the files\n")
	write(t, l, "home/mixed.md", "mixed\n")
	l.Author("home/fs.md", git.FilesystemAuthor)
	l.Author("home/mixed.md", "user:fox")
	l.Author("home/mixed.md", git.FilesystemAuthor)
	if _, err := l.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	got := authors(t, l.Root())
	if len(got) != 2 {
		t.Fatalf("authors: %v", got)
	}
	// The human window is HEAD, so the filesystem commit is older.
	if got[0] != "yana user <user@yana.local>" {
		t.Fatalf("HEAD author = %q", got[0])
	}
	if got[1] != "filesystem <"+git.FilesystemEmail+">" {
		t.Fatalf("filesystem author = %q", got[1])
	}
}

func TestActivityLogCursorPagesWithoutOverlap(t *testing.T) {
	l := newLayer(t, testOptions())
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		write(t, l, "home/n.md", fmt.Sprintf("edit %d\n", i))
		l.Author("home/n.md", "user:fox")
		if _, err := l.Snapshot(ctx); err != nil {
			t.Fatal(err)
		}
	}
	page1, err := l.ActivityLog(ctx, "home", time.Time{}, time.Time{}, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 {
		t.Fatalf("page 1 has %d commits, want 2", len(page1))
	}
	page2, err := l.ActivityLog(ctx, "home", time.Time{}, time.Time{}, page1[1].Hash, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 {
		t.Fatalf("page 2 has %d commits, want 2", len(page2))
	}
	for _, a := range page1 {
		for _, b := range page2 {
			if a.Hash == b.Hash {
				t.Fatal("pages overlap")
			}
		}
	}
	if page1[0].Date.Before(page2[0].Date) || page2[0].Date.Before(page2[1].Date) {
		t.Fatal("pages are out of order across the cursor")
	}
	// Walking below the oldest commit runs out without error.
	oldest, err := l.ActivityLog(ctx, "home", time.Time{}, time.Time{}, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	root, err := l.ActivityLog(ctx, "home", time.Time{}, time.Time{}, oldest[len(oldest)-1].Hash, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(root) != 0 {
		t.Fatalf("page below the root commit returned %d commits", len(root))
	}
	// A cursor that names nothing in this repository is refused.
	if _, err := l.ActivityLog(ctx, "home", time.Time{}, time.Time{}, "1234567890ab", 2); err == nil {
		t.Fatal("unknown cursor accepted")
	}
}

func TestActivityLogSinceUntilBoundTheWalk(t *testing.T) {
	l := newLayer(t, testOptions())
	ctx := context.Background()
	old := time.Now().Add(-48 * time.Hour)
	write(t, l, "home/old.md", "old\n")
	commitAs(t, l, old)
	write(t, l, "home/new.md", "new\n")
	commitAs(t, l, time.Now())

	since, err := l.ActivityLog(ctx, "home", time.Now().Add(-1*time.Hour), time.Time{}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 1 || since[0].Changes[0].Path != "home/new.md" {
		t.Fatalf("since filter: %+v", since)
	}
	until, err := l.ActivityLog(ctx, "home", time.Time{}, time.Now().Add(-24*time.Hour), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(until) != 1 || until[0].Changes[0].Path != "home/old.md" {
		t.Fatalf("until filter: %+v", until)
	}
	// The dates come back as real times.
	if until[0].Date.Before(old.Add(-time.Minute)) || until[0].Date.After(old.Add(time.Minute)) {
		t.Fatalf("author date %v, want about %v", until[0].Date, old)
	}
}

// commitAs commits the tree as one window stamped with author dates at
// ts, so since/until have distinct commits to cut between.
func commitAs(t testing.TB, l *git.Layer, ts time.Time) {
	t.Helper()
	runGitAt(t, l.Root(), ts, "add", "-A")
	runGitAt(t, l.Root(), ts, "-c", "user.name=YANA", "-c", "user.email=yana@local",
		"commit", "-m", "notes: test commit")
}

// runGitAt runs git with the author and committer dates pinned to ts.
func runGitAt(t testing.TB, dir string, ts time.Time, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+ts.Format(time.RFC3339),
		"GIT_COMMITTER_DATE="+ts.Format(time.RFC3339),
		"GIT_TERMINAL_PROMPT=0",
	)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out.String())
	}
	return out.String()
}
