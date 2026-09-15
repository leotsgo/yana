package reconcile

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
)

// deleteViaApp runs the in-app delete path and checks the basics: the
// file is under .trash at its own structure, the row is retired, and the
// listing sees it.
func TestDeleteMovesFileToTrash(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("proj/ideas/a.md", "# A\n\nfirst\n")
	h.converged(id)

	res, err := h.rec.Delete(h.ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.TrashPath != ".trash/proj/ideas/a.md" {
		t.Fatalf("trash path %q", res.TrashPath)
	}
	if _, err := os.Stat(h.abs("proj/ideas/a.md")); !os.IsNotExist(err) {
		t.Fatal("original file still there")
	}
	if b, err := os.ReadFile(h.abs(".trash/proj/ideas/a.md")); err != nil || !strings.Contains(string(b), "first") {
		t.Fatalf("trash copy: %v", err)
	}
	if _, err := os.Stat(h.abs(".sync/crdt/retired/" + id + ".bin")); err != nil {
		t.Fatal("sidecar not retained")
	}
	if _, err := h.db.GetNote(h.ctx, id); err != index.ErrNotFound {
		t.Fatalf("note row: %v", err)
	}

	entries, err := h.rec.ListTrash(h.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != id || entries[0].RelPath != "proj/ideas/a.md" || !entries[0].HasFile || !entries[0].HasSidecar {
		t.Fatalf("entries: %+v", entries)
	}
}

func TestDeleteDeniedWithoutWrite(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("proj/a.md", "x\n")
	h.converged(id)
	denied := func(string) error { return errDeniedTest }
	if _, err := h.rec.Delete(h.ctx, id, denied); err == nil {
		t.Fatal("delete allowed without write access")
	}
	if _, err := os.Stat(h.abs("proj/a.md")); err != nil {
		t.Fatal("file should not have moved")
	}
}

var errDeniedTest = staticErr("no")

type staticErr string

func (e staticErr) Error() string { return string(e) }

func TestRestoreReturnsFileAndReResolvesLinks(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	target := h.newNote("proj/target.md", "# Target\n")
	linker := h.newNote("proj/linker.md", "See [[target]].\n")
	h.converged(linker)

	if back, err := h.db.Backlinks(h.ctx, target); err != nil || len(back) != 1 {
		t.Fatalf("backlinks before delete: %v %v", back, err)
	}
	if _, err := h.rec.Delete(h.ctx, target, nil); err != nil {
		t.Fatal(err)
	}
	if !h.eventually(20*tSettle, func() bool {
		back, err := h.db.Backlinks(h.ctx, target)
		return err == nil && len(back) == 0
	}) {
		t.Fatal("inbound link still resolves after delete")
	}

	res, err := h.rec.Restore(h.ctx, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflict || res.Path != "proj/target.md" {
		t.Fatalf("restore result: %+v", res)
	}
	if b, err := os.ReadFile(h.abs("proj/target.md")); err != nil || !strings.Contains(string(b), "# Target") {
		t.Fatalf("restored file: %v %q", err, b)
	}
	if n, err := h.db.GetNote(h.ctx, target); err != nil || n.RelPath != "proj/target.md" {
		t.Fatalf("note row after restore: %v %+v", err, n)
	}
	if !h.eventually(20*tSettle, func() bool {
		back, err := h.db.Backlinks(h.ctx, target)
		return err == nil && len(back) == 1
	}) {
		t.Fatal("inbound link did not re-resolve after restore")
	}
	// The trash no longer lists it, and the document survives a reload.
	if entries, _ := h.rec.ListTrash(h.ctx, nil); len(entries) != 0 {
		t.Fatalf("trash after restore: %+v", entries)
	}
	if txt, err := h.rec.Text(h.ctx, target); err != nil || txt != "# Target\n" {
		t.Fatalf("document after restore: %v %q", err, txt)
	}
}

// The acceptance case: rm a note from a shell, then restore it from the
// app with the full edit history intact.
func TestExternalDeleteThenRestoreKeepsHistory(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("proj/gone.md", "# Gone\n\nline one\n")
	h.converged(id)

	// Edit through the document so there is history to keep, and let it
	// reach the file.
	if err := h.rec.SetText(h.ctx, id, "# Gone\n\nline one\nline two\n", "user:collin"); err != nil {
		t.Fatal(err)
	}
	h.converged(id)
	_, rows, err := h.db.LogStats(h.ctx, id)
	if err != nil || rows == 0 {
		t.Fatalf("log before delete: %v %d", err, rows)
	}

	// `rm` from a shell.
	if err := os.Remove(h.abs("proj/gone.md")); err != nil {
		t.Fatal(err)
	}
	if !h.eventually(20*tSettle, func() bool {
		_, err := h.db.GetNote(h.ctx, id)
		return err == index.ErrNotFound
	}) {
		t.Fatal("index row survived the external delete")
	}
	entries, err := h.rec.ListTrash(h.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != id || entries[0].HasFile || !entries[0].HasSidecar {
		t.Fatalf("entries: %+v", entries)
	}

	res, err := h.rec.Restore(h.ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != "proj/gone.md" {
		t.Fatalf("restore path %q", res.Path)
	}
	if b, err := os.ReadFile(h.abs("proj/gone.md")); err != nil || !strings.Contains(string(b), "line two") {
		t.Fatalf("rebuilt file: %v %q", err, b)
	}
	// Full history: the log rows survived the delete and restore, and
	// the document still carries them.
	_, rowsAfter, err := h.db.LogStats(h.ctx, id)
	if err != nil || rowsAfter == 0 {
		t.Fatalf("log after restore: %v %d", err, rowsAfter)
	}
	if txt, err := h.rec.Text(h.ctx, id); err != nil || txt != "# Gone\n\nline one\nline two\n" {
		t.Fatalf("document after restore: %v %q", err, txt)
	}
	if err := h.rec.SetText(h.ctx, id, "# Gone\n\nline one\nline two\nline three\n", "user:collin"); err != nil {
		t.Fatalf("edit after restore: %v", err)
	}
	h.converged(id)
}

func TestRestoreConflictLandsBeside(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("proj/a.md", "# Original\n")
	h.converged(id)
	if _, err := h.rec.Delete(h.ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	// A new note takes the original path while the old one is in trash.
	h.newNote("proj/a.md", "# Replacement\n")
	res, err := h.rec.Restore(h.ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Conflict || res.Path == "proj/a.md" || !strings.HasPrefix(res.Path, "proj/a.conflict-") {
		t.Fatalf("restore result: %+v", res)
	}
	if b, _ := os.ReadFile(h.abs("proj/a.md")); !strings.Contains(string(b), "Replacement") {
		t.Fatal("replacement note was clobbered")
	}
}

func TestDeleteSameNameTwiceKeepsBoth(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id1 := h.newNote("proj/twin.md", "# One\n")
	h.converged(id1)
	if _, err := h.rec.Delete(h.ctx, id1, nil); err != nil {
		t.Fatal(err)
	}
	id2 := h.newNote("proj/twin.md", "# Two\n")
	h.converged(id2)
	if _, err := h.rec.Delete(h.ctx, id2, nil); err != nil {
		t.Fatal(err)
	}
	entries, err := h.rec.ListTrash(h.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries: %+v", entries)
	}
	for _, e := range entries {
		if e.ID == id1 && e.TrashPath != ".trash/proj/twin.md" {
			t.Fatalf("first delete should hold the plain path, got %q", e.TrashPath)
		}
		if e.ID == id2 && !strings.Contains(e.TrashPath, ".deleted-") {
			t.Fatalf("second delete should be suffixed, got %q", e.TrashPath)
		}
	}
}

func TestDestroyAndEmptyArePermanent(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id1 := h.newNote("proj/one.md", "one\n")
	id2 := h.newNote("proj/two.md", "two\n")
	h.converged(id1)
	h.converged(id2)
	for _, id := range []string{id1, id2} {
		if _, err := h.rec.Delete(h.ctx, id, nil); err != nil {
			t.Fatal(err)
		}
	}

	// One entry destroyed alone.
	if err := h.rec.Destroy(h.ctx, id1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.abs(".trash/proj/one.md")); !os.IsNotExist(err) {
		t.Fatal("destroy left the trash copy")
	}
	if _, err := os.Stat(h.abs(".sync/crdt/retired/" + id1 + ".bin")); !os.IsNotExist(err) {
		t.Fatal("destroy left the sidecar")
	}
	if _, rows, _ := h.db.LogStats(h.ctx, id1); rows != 0 {
		t.Fatal("destroy left the log")
	}
	if _, err := h.db.GetDeleted(h.ctx, id1); err != index.ErrDeletedNotFound {
		t.Fatalf("destroy left the row: %v", err)
	}

	// Empty takes the rest.
	n, err := h.rec.EmptyTrash(h.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("emptied %d", n)
	}
	if entries, _ := h.rec.ListTrash(h.ctx, nil); len(entries) != 0 {
		t.Fatalf("trash after empty: %+v", entries)
	}
}

func TestSweepTrashAfterRetention(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("proj/old.md", "old\n")
	h.converged(id)
	if _, err := h.rec.Delete(h.ctx, id, nil); err != nil {
		t.Fatal(err)
	}

	// Age the entry past the window, then sweep.
	if err := ageDeletedRow(h, id, time.Now().Add(-31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	h.rec.sweepTrash()

	if _, err := os.Stat(h.abs(".trash/proj/old.md")); !os.IsNotExist(err) {
		t.Fatal("sweep left the trash copy")
	}
	if _, err := h.db.GetDeleted(h.ctx, id); err != index.ErrDeletedNotFound {
		t.Fatalf("sweep left the row: %v", err)
	}

	// Untracked files (a rebuilt index) age out too.
	os.MkdirAll(filepath.Dir(h.abs(".trash/proj/loose.md")), 0o755)
	os.WriteFile(h.abs(".trash/proj/loose.md"), []byte("loose\n"), 0o644)
	old := time.Now().Add(-40 * 24 * time.Hour)
	os.Chtimes(h.abs(".trash/proj/loose.md"), old, old)
	h.rec.sweepTrash()
	if _, err := os.Stat(h.abs(".trash/proj/loose.md")); !os.IsNotExist(err) {
		t.Fatal("sweep left the untracked file")
	}
}

// ageDeletedRow rewrites deleted_at so a sweep test can cross the
// retention window without waiting for it.
func ageDeletedRow(h *harness, id string, at time.Time) error {
	return h.db.Write(h.ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE deleted_notes SET deleted_at = ? WHERE id = ?`, at.UnixNano(), id)
		return err
	})
}

func TestListTrashHidesEntriesWithNothingLeft(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	h.newNote("proj/never.md", "never loaded\n")
	// The file vanishes before the document was ever loaded: no sidecar,
	// no trash copy. There is nothing to list.
	if err := os.Remove(h.abs("proj/never.md")); err != nil {
		t.Fatal(err)
	}
	if !h.eventually(20*tSettle, func() bool {
		entries, err := h.rec.ListTrash(h.ctx, nil)
		return err == nil && len(entries) == 0
	}) {
		entries, _ := h.rec.ListTrash(h.ctx, nil)
		t.Fatalf("listed an unrecoverable entry: %+v", entries)
	}
}
