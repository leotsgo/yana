package git_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/leotsgo/yana/internal/git"
	"github.com/leotsgo/yana/internal/index"
)

// gitOK reports whether a git command succeeds in dir.
func gitOK(dir string, args ...string) bool {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run() == nil
}

func putFile(t testing.TB, dir, rel, content string) {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t testing.TB, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

func markerPath(dir string) string { return filepath.Join(dir, ".sync", "restore-pending") }

func TestHoldsNoNotes(t *testing.T) {
	dir := t.TempDir()
	l := git.New(dir, testOptions(), nil)
	check := func(want bool) {
		t.Helper()
		if got, err := l.HoldsNoNotes(); err != nil || got != want {
			t.Fatalf("HoldsNoNotes = %v, %v; want %v", got, err, want)
		}
	}
	check(true)
	putFile(t, dir, ".sync/x", "state")
	putFile(t, dir, ".sync/deep/n.md", "derived, not a note")
	putFile(t, dir, "lost+found/junk.bin", "junk")
	check(true)
	putFile(t, dir, "a/n.md", "note")
	check(false)
	if err := os.RemoveAll(filepath.Join(dir, "a")); err != nil {
		t.Fatal(err)
	}
	putFile(t, dir, "_assets/p.png", "png")
	check(false)
}

func TestBootstrapFillsEmptyRoot(t *testing.T) {
	ctx := context.Background()
	bare := seedRemote(t, map[string]string{"a/one.md": "one\n", "b/two.md": "two\n"})
	dir := t.TempDir()
	putFile(t, dir, "lost+found/keep.bin", "keep")
	l := git.New(dir, testOptions(), nil)
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	var phases []git.BootstrapPhase
	commits, err := l.Bootstrap(ctx, index.GitRemote{ID: "r", Name: "backup", URL: bare, Enabled: true},
		func(p git.BootstrapPhase) { phases = append(phases, p) })
	if err != nil {
		t.Fatal(err)
	}
	if commits < 1 {
		t.Fatalf("commits = %d", commits)
	}
	if !reflect.DeepEqual(phases, []git.BootstrapPhase{git.PhaseConnecting, git.PhaseFetching}) {
		t.Fatalf("phases: %v", phases)
	}
	if readFile(t, dir, "a/one.md") != "one\n" || readFile(t, dir, "b/two.md") != "two\n" {
		t.Fatal("the backup's notes are not on disk")
	}
	if readFile(t, dir, "lost+found/keep.bin") != "keep" {
		t.Fatal("an untracked file was disturbed")
	}
	if _, err := os.Stat(markerPath(dir)); err != nil {
		t.Fatalf("marker missing before FinishBootstrap: %v", err)
	}
	if err := l.FinishBootstrap(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(markerPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("marker survived FinishBootstrap: %v", err)
	}
}

func TestBootstrapRefusesNotes(t *testing.T) {
	ctx := context.Background()
	bare := seedRemote(t, map[string]string{"a/one.md": "one\n"})
	dir := t.TempDir()
	putFile(t, dir, "x.md", "mine\n")
	l := git.New(dir, testOptions(), nil)
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := l.Bootstrap(ctx, index.GitRemote{ID: "r", Name: "backup", URL: bare, Enabled: true}, nil)
	if !errors.Is(err, git.ErrNotEmpty) {
		t.Fatalf("err = %v, want ErrNotEmpty", err)
	}
	if gitOK(dir, "rev-parse", "--verify", git.RestoreRef) {
		t.Fatal("the backup was fetched into a root that holds notes")
	}
	if _, err := os.Stat(filepath.Join(dir, "a", "one.md")); !os.IsNotExist(err) {
		t.Fatal("the backup's tree was written over notes")
	}
}

func TestBootstrapEmptyBackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	l := git.New(dir, testOptions(), nil)
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	before := runGit(t, dir, "status", "--porcelain", "--ignored")
	_, err := l.Bootstrap(ctx, index.GitRemote{ID: "r", Name: "backup", URL: bareRemote(t), Enabled: true}, nil)
	if !errors.Is(err, git.ErrEmptyBackup) {
		t.Fatalf("err = %v, want ErrEmptyBackup", err)
	}
	if after := runGit(t, dir, "status", "--porcelain", "--ignored"); after != before {
		t.Fatalf("root changed:\n%s\nwas\n%s", after, before)
	}
	if _, err := os.Stat(markerPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("marker left behind: %v", err)
	}
}

func TestBootstrapInterruptedIsRolledBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		// commit makes a commit before the bootstrap, as the window loop
		// would have, keep.bin included.
		commit bool
	}{{"unborn", false}, {"after a commit", true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			bare := seedRemote(t, map[string]string{"a/one.md": "one\n", "b/c/two.md": "two\n"})
			dir := t.TempDir()
			putFile(t, dir, "lost+found/keep.bin", "keep")
			l := git.New(dir, testOptions(), nil)
			if err := l.Ensure(ctx); err != nil {
				t.Fatal(err)
			}
			prev := ""
			if tc.commit {
				runGit(t, dir, "add", "-A")
				runGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "window")
				prev = strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
			}
			if _, err := l.Bootstrap(ctx, index.GitRemote{ID: "r", Name: "backup", URL: bare, Enabled: true}, nil); err != nil {
				t.Fatal(err)
			}
			if readFile(t, dir, "lost+found/keep.bin") != "keep" {
				t.Fatal("bootstrap disturbed a file the backup does not hold")
			}
			// The process dies here: FinishBootstrap never runs.
			again := git.New(dir, testOptions(), nil)
			if err := again.Ensure(ctx); err != nil {
				t.Fatal(err)
			}
			if empty, err := again.HoldsNoNotes(); err != nil || !empty {
				t.Fatalf("HoldsNoNotes after recovery = %v, %v", empty, err)
			}
			for _, d := range []string{"a", "b"} {
				if _, err := os.Stat(filepath.Join(dir, d)); !os.IsNotExist(err) {
					t.Fatalf("directory %s left behind: %v", d, err)
				}
			}
			if readFile(t, dir, "lost+found/keep.bin") != "keep" {
				t.Fatal("rollback disturbed a file the backup does not hold")
			}
			if _, err := os.Stat(markerPath(dir)); !os.IsNotExist(err) {
				t.Fatalf("marker survived recovery: %v", err)
			}
			if prev == "" {
				if gitOK(dir, "rev-parse", "--verify", "HEAD") {
					t.Fatal("HEAD exists after rolling back an unborn branch")
				}
				if out := runGit(t, dir, "ls-files"); out != "" {
					t.Fatalf("index not empty: %q", out)
				}
			} else {
				if head := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD")); head != prev {
					t.Fatalf("HEAD = %s, want %s", head, prev)
				}
				if !gitOK(dir, "diff", "--cached", "--quiet", "HEAD") {
					t.Fatal("index does not match HEAD after rollback")
				}
			}
		})
	}
}
