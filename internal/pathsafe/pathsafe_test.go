package pathsafe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestRoot(t *testing.T) (*Root, string) {
	t.Helper()
	dir := t.TempDir()
	// A symlinked root exercises the EvalSymlinks path.
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	r, err := NewRoot(link, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return r, real
}

func TestCleanAccepts(t *testing.T) {
	r, _ := newTestRoot(t)
	cases := map[string]string{
		"":                    "",
		".":                   "",
		"a.md":                "a.md",
		"./a.md":              "a.md",
		"space/sub/note.md":   "space/sub/note.md",
		"space//sub/note.md":  "space/sub/note.md",
		"space/./note.md":     "space/note.md",
		"naïve.md":            "naïve.md",
		"日本語/メモ.md":           "日本語/メモ.md",
		"emoji \U0001f642.md": "emoji \U0001f642.md",
		"CONTENT.md":          "CONTENT.md", // not a reserved name
		"console.md":          "console.md",
	}
	for in, want := range cases {
		got, err := r.Clean(in)
		if err != nil {
			t.Errorf("Clean(%q): unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanRejects(t *testing.T) {
	r, _ := newTestRoot(t)
	long := strings.Repeat("a", 300)
	cases := []string{
		"../x.md", "a/../../x.md", "a/..", "..", "/etc/passwd", "a\x00b.md",
		"a\nb.md", "a\tb", "con", "CON.md", "com1.md", "LPT9", "nul.md", "prn",
		"trailing.", "trailing ", " leading", "a/b./c", "back\\slash.md",
		"bad:colon.md", "q?.md", "star*.md", "pipe|.md", "lt<.md", "gt>.md", "quote\".md",
		long + ".md", strings.Repeat("a/", 600) + "x.md",
		"\xff\xfe.md", "rtl‮override.md", "zero​width.md",
	}
	for _, in := range cases {
		if _, err := r.Clean(in); err == nil {
			t.Errorf("Clean(%q) accepted, want rejection", in)
		} else if !IsRejection(err) {
			t.Errorf("Clean(%q) returned a non-rejection error: %v", in, err)
		}
	}
}

func TestNFCNormalisation(t *testing.T) {
	r, _ := newTestRoot(t)
	decomposed := "café.md" // e + combining acute
	got, err := r.Clean(decomposed)
	if err != nil {
		t.Fatal(err)
	}
	if got != "café.md" {
		t.Fatalf("expected NFC form, got %q", got)
	}
}

func TestResolveContainment(t *testing.T) {
	r, real := newTestRoot(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(real, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(real, "secret-link.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(real, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(real, "inside"), filepath.Join(real, "inside-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(real, "nowhere"), filepath.Join(real, "dangling")); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"escape/secret.md", "escape", "escape/new.md", "secret-link.md", "escape/deeper/x.md", "dangling", "dangling/x.md"} {
		if _, _, err := r.Resolve(bad); err == nil {
			t.Errorf("Resolve(%q) should be rejected", bad)
		}
	}
	for _, good := range []string{"inside/new.md", "inside-link/new.md", "inside-link", "brand/new/dir/note.md", ""} {
		abs, _, err := r.Resolve(good)
		if err != nil {
			t.Errorf("Resolve(%q): %v", good, err)
			continue
		}
		if abs != r.Dir() && !strings.HasPrefix(abs, r.Dir()+string(filepath.Separator)) {
			t.Errorf("Resolve(%q) = %q not under root", good, abs)
		}
	}
}

func TestCheckCollision(t *testing.T) {
	r, real := newTestRoot(t)
	if err := os.WriteFile(filepath.Join(real, "notes.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.CheckCollision("notes.md"); err != nil {
		t.Errorf("same name is not a collision: %v", err)
	}
	if err := r.CheckCollision("Notes.md"); err == nil {
		t.Error("case-variant name should collide")
	}
	if err := r.CheckCollision("other.md"); err != nil {
		t.Errorf("distinct name: %v", err)
	}
	if err := r.CheckCollision("missing-dir/x.md"); err != nil {
		t.Errorf("missing parent dir is not a collision: %v", err)
	}
}

func TestRateLimiter(t *testing.T) {
	l := NewRateLimiter(Rate{3, time.Minute}, Rate{1, time.Minute})
	now := time.Unix(0, 0)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !l.Allow("user:a") {
			t.Fatalf("user write %d should be allowed", i)
		}
	}
	if l.Allow("user:a") {
		t.Fatal("4th user write should be refused")
	}
	if !l.Allow("user:b") {
		t.Fatal("other users have their own bucket")
	}
	if !l.Allow("agent:bot") || l.Allow("agent:bot") {
		t.Fatal("agent rate is 1/min")
	}
	for i := 0; i < 100; i++ {
		if !l.Allow("filesystem") {
			t.Fatal("filesystem is never limited")
		}
	}
	now = now.Add(20 * time.Second) // one token refilled for user:a
	if !l.Allow("user:a") || l.Allow("user:a") {
		t.Fatal("expected exactly one refilled token")
	}
}

func TestOutsideRootError(t *testing.T) {
	r, real := newTestRoot(t)
	if err := os.Symlink("/", filepath.Join(real, "rootlink")); err != nil {
		t.Fatal(err)
	}
	_, _, err := r.Resolve("rootlink/etc")
	if err == nil || !strings.Contains(err.Error(), ErrOutsideRoot.Error()) {
		t.Fatalf("expected outside-root rejection, got %v", err)
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatal("expected *Error")
	}
}
