package reconcile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMoveRewritesInboundLinks(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()

	// The target is linked from 20 notes across the space, mixing every
	// resolution style: sibling-relative, extension spelled out, display
	// text, root-style paths, and bare filenames from other directories.
	target := h.newNote("main/guides/target.md", "# Target\n\nBody text.\n")
	type linker struct {
		id, rel string
	}
	var linkers []linker
	for i := 0; i < 20; i++ {
		var body string
		var rel string
		switch i % 4 {
		case 0: // sibling-relative, no extension
			body = "Points at [[target]] from next door.\n"
			rel = fmt.Sprintf("main/guides/g%02d.md", i)
		case 1: // sibling-relative with extension and display text
			body = "Points at [[target.md|the target]] from next door.\n"
			rel = fmt.Sprintf("main/guides/g%02d.md", i)
		case 2: // root-style path from a top-level note
			body = "Points at [[guides/target]] from afar.\n"
			rel = fmt.Sprintf("main/l%02d.md", i)
		default: // bare filename from elsewhere in the space
			body = "Points at [[target]] from afar.\n"
			rel = fmt.Sprintf("main/l%02d.md", i)
		}
		id := h.newNote(rel, body)
		linkers = append(linkers, linker{id: id, rel: rel})
	}

	// An open client on a sibling linker: the rewrite must reach it live.
	client := h.newClient("tab1", linkers[0].id)
	defer client.close()

	if _, err := h.rec.Move(h.ctx, target, "main/guides/moved.md", nil); err != nil {
		t.Fatalf("move: %v", err)
	}

	if _, err := os.Lstat(h.abs("main/guides/target.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old target file still present: %v", err)
	}
	if got := h.fileBody("main/guides/moved.md"); !strings.Contains(got, "# Target") {
		t.Fatalf("moved file body = %q", got)
	}
	if p, err := h.rec.Path(h.ctx, target); err != nil || p != "main/guides/moved.md" {
		t.Fatalf("rec.Path = %q, %v", p, err)
	}

	// Every linking note converges: document, file on disk, index, and the
	// open client all hold the rewritten target.
	for i, lk := range linkers {
		var wantRaw string
		switch i % 4 {
		case 0:
			wantRaw = "moved"
		case 1:
			wantRaw = "moved.md"
		case 2:
			wantRaw = "guides/moved"
		default:
			wantRaw = "moved"
		}
		got := h.converged(lk.id)
		if !strings.Contains(got, "[["+wantRaw) {
			t.Fatalf("linker %s did not rewrite to %q: %q", lk.rel, wantRaw, got)
		}
		if strings.Contains(got, "[[target") {
			t.Fatalf("linker %s still holds the old raw target: %q", lk.rel, got)
		}
	}
	if got := client.text(); !strings.Contains(got, "[[moved") {
		t.Fatalf("open client did not receive the rewrite live: %q", got)
	}

	// The links table resolves every rewritten link to the moved note.
	if !h.eventually(20*tSettle, func() bool {
		back, err := h.db.Backlinks(h.ctx, target)
		return err == nil && len(back) == 20
	}) {
		back, _ := h.db.Backlinks(h.ctx, target)
		t.Fatalf("backlinks after move = %d, want 20", len(back))
	}
}

func TestMoveNestedRelative(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()

	// A relative link from deep inside the tree, resolved and rewritten
	// with its directories intact.
	deep := h.newNote("main/a/b/c/deep.md", "# Deep\n\nSee [[../../../shared/peer|the peer]].\n")
	peer := h.newNote("main/shared/peer.md", "# Peer\n")

	back, err := h.db.Backlinks(h.ctx, peer)
	if err != nil || len(back) != 1 || back[0].Note.ID != deep {
		t.Fatalf("backlinks before move = %+v, %v", back, err)
	}

	if _, err := h.rec.Move(h.ctx, peer, "main/x/y/peer.md", nil); err != nil {
		t.Fatalf("move: %v", err)
	}
	got := h.converged(deep)
	want := "# Deep\n\nSee [[../../../x/y/peer|the peer]].\n"
	if got != want {
		t.Fatalf("deep body = %q, want %q", got, want)
	}
	back, err = h.db.Backlinks(h.ctx, peer)
	if err != nil || len(back) != 1 {
		t.Fatalf("backlinks after move = %+v, %v", back, err)
	}
}

func TestMoveDeniedAppliesNothing(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()

	target := h.newNote("main/guides/target.md", "# Target\n")
	h.newNote("main/linker.md", "See [[guides/target]].\n")
	events := 0
	unsub := h.rec.Subscribe(func(ev Event) {
		if ev.Kind == EventUpdate || ev.Kind == EventMoved {
			events++
		}
	})
	defer unsub()

	deny := func(space string) error {
		if space == "main" {
			return errors.New("read-only space")
		}
		return nil
	}
	_, err := h.rec.Move(h.ctx, target, "main/guides/renamed.md", deny)
	var denied *SpaceDeniedError
	if !errors.As(err, &denied) || denied.Space != "main" {
		t.Fatalf("err = %v, want SpaceDeniedError for main", err)
	}

	// Nothing changed: the file, the link, the index, the event stream.
	if _, err := os.Lstat(h.abs("main/guides/target.md")); err != nil {
		t.Fatalf("target vanished: %v", err)
	}
	if got := h.fileBody("main/linker.md"); got != "See [[guides/target]].\n" {
		t.Fatalf("linker body = %q", got)
	}
	if p, err := h.rec.Path(h.ctx, target); err != nil || p != "main/guides/target.md" {
		t.Fatalf("path = %q, %v", p, err)
	}
	back, err := h.db.Backlinks(h.ctx, target)
	if err != nil || len(back) != 1 {
		t.Fatalf("backlinks = %+v, %v", back, err)
	}
	time.Sleep(2 * tSettle)
	if events != 0 {
		t.Fatalf("%d events fired for a refused move", events)
	}
}

func TestMoveCrossSpaceCountsBroken(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()

	target := h.newNote("main/target.md", "# Target\n")
	h.newNote("main/linker.md", "See [[target]].\n")

	res, err := h.rec.Move(h.ctx, target, "other/target.md", nil)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if res.Rewritten != 0 || res.Broken != 1 {
		t.Fatalf("result = %+v, want 1 broken, 0 rewritten", res)
	}
	// The raw target is left as written; it resolves to nothing now.
	if got := h.fileBody("main/linker.md"); got != "See [[target]].\n" {
		t.Fatalf("cross-space move rewrote the linker: %q", got)
	}
	if !h.eventually(20*tSettle, func() bool {
		un, err := h.db.UnresolvedLinks(h.ctx, "main")
		return err == nil && len(un) == 1 && un[0].RawTarget == "target"
	}) {
		un, _ := h.db.UnresolvedLinks(h.ctx, "main")
		t.Fatalf("unresolved after cross-space move = %+v", un)
	}
}

func TestMoveErrors(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()

	target := h.newNote("main/t.md", "# T\n")
	h.newNote("main/taken.md", "# Taken\n")

	if _, err := h.rec.Move(h.ctx, target, "main/t.md", nil); !errors.Is(err, ErrMoveSamePath) {
		t.Fatalf("same path err = %v", err)
	}
	if _, err := h.rec.Move(h.ctx, target, "main/taken.md", nil); !errors.Is(err, ErrMoveTargetTaken) {
		t.Fatalf("taken err = %v", err)
	}
	if _, err := h.rec.Move(h.ctx, target, "main/notenote.txt", nil); err == nil {
		t.Fatal("non-note target accepted")
	}
	if _, err := h.rec.Move(h.ctx, target, "main/../../escape.md", nil); err == nil {
		t.Fatal("escaping target accepted")
	}
	if _, err := h.rec.Move(h.ctx, "nosuchid00000000000000", "main/x.md", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing note err = %v", err)
	}
}

func TestMoveLoadedNoteKeepsPendingEdits(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()

	target := h.newNote("main/t.md", "# T\n")
	// Load the document and leave an edit pending, then move.
	if _, err := h.rec.Text(h.ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := h.rec.SetText(h.ctx, target, "# T\n\npending edit\n", "user:alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.rec.Move(h.ctx, target, "main/deep/renamed.md", nil); err != nil {
		t.Fatalf("move: %v", err)
	}
	got := h.converged(target)
	if got != "# T\n\npending edit\n" {
		t.Fatalf("body after move = %q", got)
	}
	if p, err := h.rec.Path(h.ctx, target); err != nil || p != "main/deep/renamed.md" {
		t.Fatalf("path = %q, %v", p, err)
	}
}

// The rewrites go through write-back, and their file writes come back
// through the watcher as echoes. The loop must settle, not oscillate.
func TestMoveSettlesWithoutRewriteLoops(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()

	target := h.newNote("main/t.md", "# T\n")
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, h.newNote(fmt.Sprintf("main/l%02d.md", i), fmt.Sprintf("Link [[t]] number %d.\n", i)))
	}
	if _, err := h.rec.Move(h.ctx, target, "main/renamed.md", nil); err != nil {
		t.Fatalf("move: %v", err)
	}
	for _, id := range ids {
		h.converged(id)
	}
	before := h.rec.Stats()
	time.Sleep(10 * tSettle)
	after := h.rec.Stats()
	if after.Writebacks > before.Writebacks+int64(len(ids)) {
		t.Fatalf("write-backs kept firing: before=%d after=%d", before.Writebacks, after.Writebacks)
	}
}

func TestReplaceRawTarget(t *testing.T) {
	cases := []struct {
		name               string
		in, old, new, want string
	}{
		{"plain", "a [[target]] b\n", "target", "renamed", "a [[renamed]] b\n"},
		{"display kept", "[[target|My Text]]\n", "target", "renamed", "[[renamed|My Text]]\n"},
		{"with extension", "[[target.md]]\n", "target.md", "renamed.md", "[[renamed.md]]\n"},
		{"prefix target untouched", "[[targets]]\n", "target", "renamed", "[[targets]]\n"},
		{"extension is not a prefix match", "[[target]]\n", "target.md", "renamed.md", "[[target]]\n"},
		{"multiple", "[[target]] and [[target|x]]\n", "target", "renamed", "[[renamed]] and [[renamed|x]]\n"},
		{"fenced code untouched", "before\n```md\n[[target]]\n```\nafter [[target]]\n", "target", "renamed", "before\n```md\n[[target]]\n```\nafter [[renamed]]\n"},
		{"no trailing newline", "x [[target]]", "target", "renamed", "x [[renamed]]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, hit := replaceRawTarget(tc.in, tc.old, tc.new)
			if got != tc.want {
				t.Fatalf("replaceRawTarget(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if hit != (tc.in != tc.want) {
				t.Fatalf("hit = %v, want %v", hit, tc.in != tc.want)
			}
		})
	}
}

var _ = context.Background
