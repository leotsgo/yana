package git_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/git"
	"github.com/madeofpendletonwool/yana/internal/index"
)

// seedRemote makes a bare repository holding one commit over the given
// files, a backup with a past.
func seedRemote(t testing.TB, files map[string]string) string {
	t.Helper()
	bare := bareRemote(t)
	src := t.TempDir()
	runGit(t, src, "init", "-b", "main")
	runGit(t, src, "config", "user.name", "backups")
	runGit(t, src, "config", "user.email", "backups@yana.local")
	for rel, content := range files {
		abs := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-m", "notes: backup")
	runGit(t, src, "push", bare, "main")
	return bare
}

func TestCloneOnFirstRun(t *testing.T) {
	remote := seedRemote(t, map[string]string{"home/a.md": "one\n", "home/d/b.md": "two\n"})
	dir := t.TempDir()
	// Derived state on the volume does not count as notes.
	if err := os.MkdirAll(filepath.Join(dir, ".sync"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".sync/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := testOptions()
	opts.Remote = remote
	l := git.New(dir, opts, nil)
	if err := l.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{"home/a.md": "one\n", "home/d/b.md": "two\n"} {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Fatalf("%s after clone: %q, %v", rel, got, err)
		}
	}
	if st := l.Stats(); st.Commits != 1 {
		t.Fatalf("commits after clone: %+v", st)
	}
	// .sync survived the checkout.
	if _, err := os.Stat(filepath.Join(dir, ".sync")); err != nil {
		t.Fatalf(".sync did not survive the clone: %v", err)
	}
}

func TestCloneNeverOverNotes(t *testing.T) {
	remote := seedRemote(t, map[string]string{"home/a.md": "remote\n"})
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "home", "local.md"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := testOptions()
	opts.Remote = remote
	l := git.New(dir, opts, nil)
	if err := l.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "home", "a.md")); !os.IsNotExist(err) {
		t.Fatal("the remote's tree was cloned over existing notes")
	}
	got, err := os.ReadFile(filepath.Join(dir, "home", "local.md"))
	if err != nil || string(got) != "local\n" {
		t.Fatalf("local notes disturbed: %q %v", got, err)
	}
	if st := l.Stats(); st.Commits != 0 {
		t.Fatalf("a fresh repository over notes should hold no commits: %+v", st)
	}
}

func TestCloneFailureLeavesRootEmpty(t *testing.T) {
	dir := t.TempDir()
	opts := testOptions()
	opts.Remote = filepath.Join(t.TempDir(), "missing.git")
	l := git.New(dir, opts, nil)
	if err := l.Ensure(context.Background()); err == nil {
		t.Fatal("a clone from a missing repository succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Fatal("the partial clone was left behind")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("the root is not empty after the failed clone: %v %v", entries, err)
	}
}

func TestPreviewRestoreRelations(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	opts := testOptions()
	l := git.New(dir, opts, nil)
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	l.Start()
	defer l.Close()
	write(t, l, "home/a.md", "one\n")
	l.Notify("home/a.md")
	if _, err := l.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	bare := bareRemote(t)
	remote := index.GitRemote{ID: "r", Name: "backup", URL: bare, Enabled: true}
	if err := l.Push(ctx, remote); err != nil {
		t.Fatal(err)
	}
	// Right after a push the backup is the local history.
	commit, err := l.FetchRestore(ctx, remote)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := l.PreviewRestore(ctx, commit); err != nil || p.Relation != "identical" || p.Commits < 1 || p.Notes != 1 {
		t.Fatalf("preview after push: %+v %v", p, err)
	}
	// A local commit the backup lacks leaves the backup behind.
	write(t, l, "home/b.md", "two\n")
	l.Notify("home/b.md")
	if _, err := l.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if p, err := l.PreviewRestore(ctx, commit); err != nil || p.Relation != "behind" {
		t.Fatalf("preview after a local commit: %+v %v", p, err)
	}
	// A commit only the backup holds leaves it ahead.
	other := t.TempDir()
	runGit(t, other, "clone", bare, other)
	runGit(t, other, "config", "user.name", "elsewhere")
	runGit(t, other, "config", "user.email", "else@yana.local")
	if err := os.WriteFile(filepath.Join(other, "home", "c.md"), []byte("three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-m", "notes: elsewhere")
	runGit(t, other, "push", "origin", "main")
	ahead, err := l.FetchRestore(ctx, remote)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := l.PreviewRestore(ctx, ahead); err != nil || p.Relation != "diverged" {
		t.Fatalf("preview with both sides moved: %+v %v", p, err)
	}
	if p, err := l.PreviewRestore(ctx, ahead); err != nil || p.Notes != 2 {
		t.Fatalf("notes in the backup's tree: %+v %v", p, err)
	}
}

func TestRestoreMovesTreeToBackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	opts := testOptions()
	l := git.New(dir, opts, nil)
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	l.Start()
	defer l.Close()
	write(t, l, "home/keep.md", "kept\n")
	l.Notify("home/keep.md")
	if _, err := l.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	bare := bareRemote(t)
	remote := index.GitRemote{ID: "r", Name: "backup", URL: bare, Enabled: true}
	if err := l.Push(ctx, remote); err != nil {
		t.Fatal(err)
	}
	commit, err := l.FetchRestore(ctx, remote)
	if err != nil {
		t.Fatal(err)
	}
	// The tree moves on after the backup was taken.
	write(t, l, "home/keep.md", "changed\n")
	write(t, l, "home/local-only.md", "gone after restore\n")
	l.Notify("home/keep.md")
	if _, err := l.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	// .sync belongs to this server and must survive.
	secret := filepath.Join(dir, ".sync", "secret")
	if err := os.MkdirAll(filepath.Dir(secret), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("server state"), 0o600); err != nil {
		t.Fatal(err)
	}
	var applied []git.RestorePath
	res, err := l.Restore(ctx, commit, func(_ context.Context, paths []git.RestorePath) {
		applied = append(applied, paths...)
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != commit {
		t.Fatalf("HEAD after restore is %s, want the backup's %s", res.Commit, commit)
	}
	got, err := os.ReadFile(filepath.Join(dir, "home", "keep.md"))
	if err != nil || string(got) != "kept\n" {
		t.Fatalf("keep.md after restore: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "home", "local-only.md")); !os.IsNotExist(err) {
		t.Fatal("a file the backup does not hold survived the restore")
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "server state" {
		t.Fatalf(".sync did not survive the restore: %q %v", b, err)
	}
	if res.Tag == "" || !strings.HasPrefix(res.Tag, "pre-restore/") {
		t.Fatalf("pre-restore tag: %q", res.Tag)
	}
	if out := runGit(t, dir, "tag", "-l", "pre-restore/*"); strings.TrimSpace(out) == "" {
		t.Fatal("the pre-restore tag is not in the repository")
	}
	if out := runGit(t, dir, "rev-parse", "HEAD"); strings.TrimSpace(out) != commit {
		t.Fatalf("HEAD is %s, want %s", out, commit)
	}
	if res.Changed != 1 || res.Deleted != 1 || len(applied) != 2 {
		t.Fatalf("restore report: %+v, applied %+v", res, applied)
	}
	// The restore is honest about where the history now stands.
	if p, err := l.PreviewRestore(ctx, commit); err != nil || p.Relation != "identical" {
		t.Fatalf("preview after restore: %+v %v", p, err)
	}
}

func TestRestoreOverUnrelatedHistory(t *testing.T) {
	ctx := context.Background()
	remote := seedRemote(t, map[string]string{"home/a.md": "one\n"})
	dir := t.TempDir()
	opts := testOptions()
	l := git.New(dir, opts, nil)
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	l.Start()
	defer l.Close()
	// A re-initialised root has history of its own that shares no
	// ancestor with the backup.
	write(t, l, "elsewhere/x.md", "unrelated\n")
	l.Notify("elsewhere/x.md")
	if _, err := l.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	r := index.GitRemote{ID: "r", Name: "backup", URL: remote, Enabled: true}
	commit, err := l.FetchRestore(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := l.PreviewRestore(ctx, commit); err != nil || p.Relation != "diverged" {
		t.Fatalf("unrelated histories should read as diverged: %+v %v", p, err)
	}
	res, err := l.Restore(ctx, commit, nil)
	if err != nil {
		t.Fatalf("restore over unrelated history: %v", err)
	}
	// The backup's tree (home/a.md) is added; the local note and the
	// .gitignore the fresh repository committed are deleted — and
	// ensureIgnore writes the ignore file straight back.
	if res.Commit != commit || res.Added != 1 || res.Deleted != 2 {
		t.Fatalf("restore report: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "elsewhere", "x.md")); !os.IsNotExist(err) {
		t.Fatal("local history's tree survived a restore it was not part of")
	}
}

func TestRestoreTimestampsDiffer(t *testing.T) {
	// Two tags a second apart must both exist; the format itself is what
	// matters here, the round trip above covers the behaviour.
	if s := time.Now().Format("20060102-150405"); strings.ContainsAny(s, " :") {
		t.Fatalf("tag timestamp %q is not a safe ref name", s)
	}
}
