package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/git"
	"github.com/madeofpendletonwool/yana/internal/index"
)

func testOptions() git.Options {
	return git.Options{Quiet: 60 * time.Millisecond, Interval: time.Hour}
}

func newLayer(t testing.TB, opts git.Options) *git.Layer {
	t.Helper()
	l := git.New(t.TempDir(), opts, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	return l
}

func write(t testing.TB, l *git.Layer, rel, content string) {
	t.Helper()
	abs := filepath.Join(l.Root(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

// gitOutput runs git in dir and returns its combined output without
// failing the test, for polling.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func authors(t testing.TB, dir string) []string {
	t.Helper()
	out := runGit(t, dir, "log", "--format=%an <%ae>")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func TestEnsureInitsRepoAndIgnoresDerivedState(t *testing.T) {
	l := newLayer(t, testOptions())
	if _, err := os.Stat(filepath.Join(l.Root(), ".git")); err != nil {
		t.Fatalf("no repository: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(l.Root(), ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".sync/", "*.yana-tmp-*"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf(".gitignore %q is missing %q", raw, want)
		}
	}
	// A hand-written ignore line survives; Ensure does not duplicate.
	ignore := filepath.Join(l.Root(), ".gitignore")
	if err := os.WriteFile(ignore, append(raw, []byte("secrets/\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(ignore)
	if got := strings.Count(string(raw2), ".sync/"); got != 1 {
		t.Fatalf(".gitignore duplicated the .sync/ line %d times: %q", got, raw2)
	}
	if !strings.Contains(string(raw2), "secrets/") {
		t.Fatal("user's ignore line was dropped")
	}
}

func TestSnapshotSplitsWindowByAuthor(t *testing.T) {
	l := newLayer(t, testOptions())
	write(t, l, "home/agent.md", "agent work\n")
	write(t, l, "home/human.md", "human work\n")
	l.Author("home/agent.md", "agent:claude")
	l.Author("home/human.md", "user:fox-12")
	l.Author("home/human.md", "filesystem") // mixed window for this path
	n, err := l.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("got %d commits, want 2 (one per author)", n)
	}
	got := authors(t, l.Root())
	if len(got) != 2 {
		t.Fatalf("authors: %v", got)
	}
	// git log is newest first: agents commit earlier in the sequence, so
	// the human window is HEAD.
	if got[0] != "yana user <user@yana.local>" {
		t.Fatalf("HEAD author = %q, want the configured human identity", got[0])
	}
	if got[1] != "claude <agent@local>" {
		t.Fatalf("older commit author = %q, want claude <agent@local>", got[1])
	}
	// The .gitignore itself is part of the first snapshot; it lands in the
	// human (fallback) group like any unattributed path.
	out := runGit(t, l.Root(), "ls-files")
	if !strings.Contains(out, ".gitignore") || !strings.Contains(out, "home/agent.md") {
		t.Fatalf("tracked files look wrong:\n%s", out)
	}
}

func TestNoSnapshotWhenClean(t *testing.T) {
	l := newLayer(t, testOptions())
	l.Snapshot(context.Background()) // first commit: .gitignore
	before := len(authors(t, l.Root()))
	n, err := l.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("committed %d times on a clean tree", n)
	}
	if after := len(authors(t, l.Root())); after != before {
		t.Fatal("a clean tree gained commits")
	}
}

func TestQuietWindowCommitsOnce(t *testing.T) {
	opts := testOptions()
	opts.Quiet = 80 * time.Millisecond
	l := git.New(t.TempDir(), opts, nil)
	ctx := context.Background()
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	l.Start()
	defer l.Close()
	// A burst of writes with activity pings: one commit once quiet.
	for i := 0; i < 5; i++ {
		write(t, l, "home/journal.md", "line\n"+strings.Repeat("x", i+1)+"\n")
		l.Notify("home/journal.md")
		time.Sleep(15 * time.Millisecond)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(authors(t, l.Root())) >= 2 { // .gitignore commit + journal commit
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	l.Close()
	if got := len(authors(t, l.Root())); got != 2 {
		t.Fatalf("got %d commits for one quiet window, want 2 (baseline + window): %v", got, authors(t, l.Root()))
	}
}

func TestContinuousEditingIsBoundedByInterval(t *testing.T) {
	opts := git.Options{Quiet: 10 * time.Second, Interval: 250 * time.Millisecond}
	l := git.New(t.TempDir(), opts, nil)
	ctx := context.Background()
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	l.Start()
	// An hour of editing scaled down: writes every 20ms for 600ms with
	// the quiet window never reached. Only the interval commits.
	for i := 0; i < 30; i++ {
		write(t, l, "home/busy.md", "edit\n"+strings.Repeat("y", i+1)+"\n")
		l.Notify("home/busy.md")
		time.Sleep(20 * time.Millisecond)
	}
	l.Close() // shutdown commits what is pending
	got := len(authors(t, l.Root()))
	// Baseline + up to three interval commits + the shutdown commit.
	if got > 5 {
		t.Fatalf("%d commits for a continuously edited window; the interval bound failed", got)
	}
	if got < 2 {
		t.Fatalf("%d commits; the interval never committed", got)
	}
}

func TestLogFollowsRenamesAndShowFindsOldContent(t *testing.T) {
	l := newLayer(t, testOptions())
	ctx := context.Background()
	write(t, l, "home/note.md", "first\n")
	l.Snapshot(ctx)
	write(t, l, "home/note.md", "first\nsecond\n")
	l.Snapshot(ctx)
	// mv the note; the watcher would report both halves, the layer just
	// sees the tree.
	abs := filepath.Join(l.Root(), "home")
	if err := os.Rename(filepath.Join(abs, "note.md"), filepath.Join(abs, "renamed.md")); err != nil {
		t.Fatal(err)
	}
	l.Snapshot(ctx)

	entries, err := l.Log(ctx, "home/renamed.md", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3 (rename followed): %+v", len(entries), entries)
	}
	// The oldest entry's path is the pre-rename one; Show reads the note
	// there.
	oldest := entries[len(entries)-1]
	if oldest.Path != "home/note.md" {
		t.Fatalf("oldest entry path = %q, want home/note.md", oldest.Path)
	}
	if oldest.Name != "yana user" {
		t.Fatalf("author name = %q", oldest.Name)
	}
	content, err := l.Show(ctx, oldest.Hash, oldest.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "first\n") || strings.Contains(string(content), "second") {
		t.Fatalf("content at oldest revision = %q", content)
	}

	diff, err := l.Diff(ctx, "home/renamed.md", entries[len(entries)-1].Hash, entries[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+second") {
		t.Fatalf("diff between oldest and newest misses the edit:\n%s", diff)
	}

	if git.ValidRevision("HEAD") || git.ValidRevision("main~1") || git.ValidRevision("") {
		t.Fatal("revision validation accepts non-hashes")
	}
	if !git.ValidRevision(entries[0].Hash) {
		t.Fatal("revision validation rejects a real hash")
	}
}

// A restart must not zero the history: a layer opened over an existing
// repository reports how many commits it holds and when the newest was
// made, and a remote's recorded push is the last push even though this
// process has not pushed.
func TestStatsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	first := git.New(dir, testOptions(), nil)
	if err := first.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	write(t, first, "home/a.md", "one\n")
	if n, err := first.Snapshot(ctx); err != nil || n != 1 {
		t.Fatalf("first snapshot: %d %v", n, err)
	}
	write(t, first, "home/b.md", "two\n")
	if n, err := first.Snapshot(ctx); err != nil || n != 1 {
		t.Fatalf("second snapshot: %d %v", n, err)
	}
	if st := first.Stats(); st.Commits != 2 || st.LastCommit.IsZero() {
		t.Fatalf("stats before restart: %+v", st)
	}

	db, err := index.Open(filepath.Join(dir, ".sync", "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	pushed := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := db.CreateGitRemote(ctx, index.GitRemote{
		ID: "r1", Name: "backup", URL: bareRemote(t), Schedule: git.ScheduleNightly, Enabled: true, CreatedAt: pushed,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordGitPush(ctx, "r1", pushed, nil); err != nil {
		t.Fatal(err)
	}

	opts := testOptions()
	opts.DB = db
	second := git.New(dir, opts, nil)
	if err := second.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	st := second.Stats()
	if st.Commits != 2 {
		t.Fatalf("commits after restart: %d", st.Commits)
	}
	if want := strconv.FormatInt(st.LastCommit.Unix(), 10); strings.TrimSpace(runGit(t, dir, "log", "-1", "--format=%ct")) != want {
		t.Fatalf("last commit after restart: %v does not match the repository", st.LastCommit)
	}
	if !st.LastPush.Equal(pushed) || st.Pushes != 0 {
		t.Fatalf("push stats after restart: %+v", st)
	}
	write(t, second, "home/c.md", "three\n")
	if n, err := second.Snapshot(ctx); err != nil || n != 1 {
		t.Fatalf("snapshot after restart: %d %v", n, err)
	}
	if st := second.Stats(); st.Commits != 3 {
		t.Fatalf("commits after a new snapshot: %d", st.Commits)
	}
}
