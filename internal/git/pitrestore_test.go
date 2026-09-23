package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/git"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
)

// reconcileOpts are the reconciler timings the restore tests need: fast
// enough to converge inside a test, slow enough to settle.
func reconcileOpts() reconcile.Options {
	return reconcile.Options{IdleTime: 100 * time.Millisecond, Debounce: 30 * time.Millisecond, SettleTime: 300 * time.Millisecond, UnloadAfter: -1}
}

// TestPreviewPITIsExact covers the preview contract: the counts and the
// list say exactly what the restore then does.
func TestPreviewPITIsExact(t *testing.T) {
	s := newStack(t, git.Options{Quiet: time.Hour, Interval: time.Hour},
		reconcileOpts())
	defer s.close(t)

	s.newNote(t, "home/keep.md", "kept\n")
	id := s.newNote(t, "home/old.md", "old text\n")
	s.newNote(t, "home/gone.md", "to be deleted\n")
	if _, err := s.gl.Snapshot(s.ctx); err != nil {
		t.Fatal(err)
	}
	base, err := s.gl.Log(s.ctx, "home/old.md", 1)
	if err != nil || len(base) != 1 {
		t.Fatalf("baseline log: %v %+v", err, base)
	}

	// Edit one, delete one, add one, rename one.
	if err := s.rec.SetText(s.ctx, id, "new text\n", "user:fox"); err != nil {
		t.Fatal(err)
	}
	s.eventually(t, 5*time.Second, func() bool { return fileBodyAt(s.dir, "home/old.md") == "new text\n" })
	os.Remove(filepath.Join(s.dir, "home", "gone.md"))
	s.newNote(t, "home/fresh.md", "arrived after\n")
	if err := os.Rename(filepath.Join(s.dir, "home", "keep.md"), filepath.Join(s.dir, "home", "moved.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.gl.Snapshot(s.ctx); err != nil {
		t.Fatal(err)
	}

	p, err := s.gl.PreviewPIT(s.ctx, base[0].Hash, "")
	if err != nil {
		t.Fatal(err)
	}
	// Restoring to the baseline: gone.md returns (added), old.md takes
	// its old text (changed), fresh.md goes (deleted), keep.md moves
	// home (moved).
	if p.Space != "" || p.Added != 1 || p.Changed != 1 || p.Deleted != 1 || p.Moved != 1 {
		t.Fatalf("preview counts: %+v", p)
	}
	if p.Author != "yana user" || p.Subject == "" || p.Date == "" {
		t.Fatalf("preview describes the commit: %+v", p)
	}
	byPath := map[string]byte{}
	for _, c := range p.Changes {
		byPath[c.Path] = c.Status
	}
	if byPath["home/gone.md"] != 'A' || byPath["home/fresh.md"] != 'D' || byPath["home/old.md"] != 'M' || byPath["home/keep.md"] != 'R' {
		t.Fatalf("preview changes: %+v", p.Changes)
	}
	for _, c := range p.Changes {
		if c.Status == 'R' && c.Path == "home/keep.md" && c.Orig != "home/moved.md" {
			t.Fatalf("rename keeps its origin: %+v", c)
		}
	}

	// The scoped preview of the other space sees nothing.
	scoped, err := s.gl.PreviewPIT(s.ctx, base[0].Hash, "work")
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Changes) != 0 || scoped.Added+scoped.Changed+scoped.Deleted+scoped.Moved != 0 {
		t.Fatalf("scope keeps other spaces out: %+v", scoped)
	}

	// The restore does what the preview said.
	var trashed, synced []string
	res, err := s.gl.RestoreTo(s.ctx, base[0].Hash, "", git.PITAuthor{Name: "fox", Email: "fox@yana.local"}, git.PITApply{
		Trash: func(ctx context.Context, rel string) error {
			abs := filepath.Join(s.dir, filepath.FromSlash(rel))
			target := filepath.Join(s.dir, ".trash", filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			trashed = append(trashed, rel)
			return os.Rename(abs, target)
		},
		Sync: func(ctx context.Context, rel string) { synced = append(synced, rel) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != p.Added || res.Changed != p.Changed || res.Deleted != p.Deleted || res.Moved != p.Moved {
		t.Fatalf("restore counts %d/%d/%d/%d, preview said %d/%d/%d/%d",
			res.Added, res.Changed, res.Deleted, res.Moved, p.Added, p.Changed, p.Deleted, p.Moved)
	}
	if got := fileBodyAt(s.dir, "home/old.md"); got != "old text\n" {
		t.Fatalf("edited note restored to the commit's text: %q", got)
	}
	if got := fileBodyAt(s.dir, "home/gone.md"); got != "to be deleted\n" {
		t.Fatalf("deleted note came back: %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "home", "keep.md")); err != nil {
		t.Fatal("the rename went home")
	}
	if _, err := os.Stat(filepath.Join(s.dir, "home", "moved.md")); err == nil {
		t.Fatal("the renamed-away copy left with the restore")
	}
	if got := fileBodyAt(s.dir, "home/fresh.md"); got != "<missing>" {
		t.Fatalf("note added after the commit should be gone, body %q", got)
	}
	// The trash holds exactly what the restore removed: the note that
	// arrived after the commit, and the renamed-away copy.
	for _, want := range []string{"home/fresh.md", "home/moved.md"} {
		found := false
		for _, rel := range trashed {
			if rel == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected %s in the trashed paths, got %v", want, trashed)
		}
	}

	// The history stays linear and honest: the newest commit is the
	// restore, authored by who ran it, and the tag marks the pre-state.
	out := runGit(t, s.dir, "log", "-3", "--format=%an <%ae> %s")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "fox <fox@yana.local> restore the tree to ") {
		t.Fatalf("restore commit: %v", lines)
	}
	if res.Tag == "" {
		t.Fatal("no pre-restore tag reported")
	}
	runGit(t, s.dir, "rev-parse", "--verify", res.Tag)
	if res.Commit == base[0].Hash {
		t.Fatal("the branch moved backwards; a restore must land as new work")
	}

	// A second look at the same commit after the restore: the tree now
	// stands there, so there is nothing left to do.
	again, err := s.gl.PreviewPIT(s.ctx, base[0].Hash, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.Added+again.Changed+again.Deleted+again.Moved != 0 {
		t.Fatalf("second preview found work: %+v", again)
	}
	res2, err := s.gl.RestoreTo(s.ctx, base[0].Hash, "", git.PITAuthor{Name: "fox", Email: "fox@yana.local"}, git.PITApply{})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Tag != "" || res2.Added+res2.Changed+res2.Deleted+res2.Moved != 0 {
		t.Fatalf("no-op restore tagged or counted: %+v", res2)
	}
}

// TestRestoreToScopedSpaceLeavesTheRestAlone: a space restore touches
// only that space's directory.
func TestRestoreToScopedSpaceLeavesTheRestAlone(t *testing.T) {
	s := newStack(t, git.Options{Quiet: time.Hour, Interval: time.Hour},
		reconcileOpts())
	defer s.close(t)

	s.newNote(t, "home/a.md", "home a\n")
	s.newNote(t, "work/b.md", "work b\n")
	if _, err := s.gl.Snapshot(s.ctx); err != nil {
		t.Fatal(err)
	}
	base, err := s.gl.Log(s.ctx, "home/a.md", 1)
	if err != nil || len(base) != 1 {
		t.Fatalf("baseline log: %v", err)
	}

	if err := s.rec.SetText(s.ctx, mustNoteID(t, s, "home/a.md"), "home a changed\n", "user:fox"); err != nil {
		t.Fatal(err)
	}
	s.newNote(t, "work/c.md", "work c\n")
	s.eventually(t, 5*time.Second, func() bool { return fileBodyAt(s.dir, "home/a.md") == "home a changed\n" })
	if _, err := s.gl.Snapshot(s.ctx); err != nil {
		t.Fatal(err)
	}

	res, err := s.gl.RestoreTo(s.ctx, base[0].Hash, "home", git.PITAuthor{Name: "fox", Email: "fox@yana.local"}, git.PITApply{
		Trash: func(ctx context.Context, rel string) error {
			target := filepath.Join(s.dir, ".trash", filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.Rename(filepath.Join(s.dir, filepath.FromSlash(rel)), target)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The space restore touches home only: a.md returns to its old
	// text, and work — edited and added to after the commit — stands
	// exactly as it is.
	if res.Changed != 1 || res.Added != 0 || res.Deleted != 0 || res.Moved != 0 {
		t.Fatalf("scoped counts: %+v", res)
	}
	if got := fileBodyAt(s.dir, "home/a.md"); got != "home a\n" {
		t.Fatalf("home restored: %q", got)
	}
	if got := fileBodyAt(s.dir, "work/b.md"); got != "work b\n" {
		t.Fatalf("work b touched: %q", got)
	}
	if got := fileBodyAt(s.dir, "work/c.md"); got != "work c\n" {
		t.Fatalf("work c touched: %q", got)
	}
	out := runGit(t, s.dir, "log", "-1", "--format=%an %s")
	if out != "fox restore home to "+base[0].Hash[:7]+"\n" {
		t.Fatalf("scoped subject: %q", out)
	}
}

// TestPreviewPITRefusesUnknownCommits: a hash that is not in this
// history is refused, not guessed at.
func TestPreviewPITRefusesUnknownCommits(t *testing.T) {
	s := newStack(t, git.Options{Quiet: time.Hour, Interval: time.Hour}, reconcileOpts())
	defer s.close(t)
	s.newNote(t, "home/x.md", "x\n")
	s.gl.Snapshot(s.ctx)
	if _, err := s.gl.PreviewPIT(s.ctx, strings.Repeat("ab", 20), ""); !errors.Is(err, git.ErrNoSuchCommit) {
		t.Fatalf("unknown commit: %v", err)
	}
	if _, err := s.gl.PreviewPIT(s.ctx, "HEAD", ""); err == nil {
		t.Fatal("a non-hash revision was accepted")
	}
}

// TestFindNoteReturnsTheDeletedContent: the history fallback for a
// deleted note finds the note's own last content, not whatever else
// lived at the path.
func TestFindNoteReturnsTheDeletedContent(t *testing.T) {
	s := newStack(t, git.Options{Quiet: time.Hour, Interval: time.Hour}, reconcileOpts())
	defer s.close(t)

	id := s.newNote(t, "home/doomed.md", "final form\n")
	s.eventually(t, 5*time.Second, func() bool { return fileBodyAt(s.dir, "home/doomed.md") == "final form\n" })
	if _, err := s.gl.Snapshot(s.ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.dir, "home", "doomed.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.gl.Snapshot(s.ctx); err != nil {
		t.Fatal(err)
	}

	content, rev, err := s.gl.FindNote(s.ctx, "home/doomed.md", id)
	if err != nil {
		t.Fatal(err)
	}
	if fm := frontmatter.Parse(content); fm.Meta.ID != id || string(fm.Body) != "final form\n" {
		t.Fatalf("found content: id %q body %q", fm.Meta.ID, fm.Body)
	}
	if rev == "" {
		t.Fatal("no revision reported")
	}
	if _, _, err := s.gl.FindNote(s.ctx, "home/doomed.md", "01ARZ3NDEKTSV4RRFFQ69G5FAV"); !errors.Is(err, git.ErrNoteNotInHistory) {
		t.Fatalf("wrong id: %v", err)
	}
	if _, _, err := s.gl.FindNote(s.ctx, "home/never-existed.md", id); !errors.Is(err, git.ErrNoteNotInHistory) {
		t.Fatalf("unknown path: %v", err)
	}
}

// --- helpers -----------------------------------------------------------------

func fileBodyAt(dir, rel string) string {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return "<missing>"
	}
	return string(frontmatter.Parse(b).Body)
}

func mustNoteID(t *testing.T, s *stack, rel string) string {
	t.Helper()
	n, err := s.db.GetNoteByPath(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	return n.ID
}
