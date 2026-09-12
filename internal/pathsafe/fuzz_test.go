package pathsafe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzResolve is the Phase 1a acceptance test: hostile inputs must never
// resolve outside the root and must never panic. Run with
// `go test -fuzz=FuzzResolve ./internal/pathsafe/` for a long session; the
// seed corpus below runs on every ordinary `go test`.
func FuzzResolve(f *testing.F) {
	seeds := []string{
		"", ".", "..", "../", "a/../..", "./../x", "/abs", "//x", "a//b",
		"a\x00b", "a\\..\\b", "CON", "con.md", "COM1.md", "LPT1", "nul",
		"trailing.", "trailing ", "..%2f", "%2e%2e/", "‥x", "..∕x",
		"a/‮/b", "café", "Ａ.md", strings.Repeat("a", 5000),
		strings.Repeat("../", 200) + "etc/passwd", "a/b/c/d/e/f/g/h/../../../../../../../../../..",
		"\xff", "a ", ".hidden/../..", "sym/../../x", "sym/x", "up/x", "up/root/x",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	dir, err := os.MkdirTemp("", "pathsafe-fuzz")
	if err != nil {
		f.Fatal(err)
	}
	defer os.RemoveAll(dir)
	real := filepath.Join(dir, "root")
	if err := os.Mkdir(real, 0o755); err != nil {
		f.Fatal(err)
	}
	outside := filepath.Join(dir, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		f.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(real, "sym")); err != nil {
		f.Fatal(err)
	}
	if err := os.Symlink("..", filepath.Join(real, "up")); err != nil {
		f.Fatal(err)
	}
	root, err := NewRoot(real, DefaultLimits())
	if err != nil {
		f.Fatal(err)
	}
	realRoot, _ := filepath.EvalSymlinks(real)

	f.Fuzz(func(t *testing.T, in string) {
		abs, rel, err := root.Resolve(in)
		if err != nil {
			if !IsRejection(err) {
				t.Fatalf("non-rejection error for %q: %v", in, err)
			}
			return
		}
		for _, seg := range strings.Split(rel, "/") {
			if seg == ".." {
				t.Fatalf("accepted rel with .. segment: %q -> %q", in, rel)
			}
		}
		if abs != root.Dir() && !strings.HasPrefix(abs, root.Dir()+string(filepath.Separator)) {
			t.Fatalf("abs outside root: %q -> %q", in, abs)
		}
		// Resolve the accepted path as far as it exists and assert containment
		// against the real root, mirroring what a write would do.
		existing := abs
		for {
			if _, err := os.Lstat(existing); err == nil {
				break
			}
			existing = filepath.Dir(existing)
		}
		resolved, err := filepath.EvalSymlinks(existing)
		if err != nil {
			t.Fatalf("accepted path with unresolvable prefix: %q -> %q: %v", in, abs, err)
		}
		if resolved != realRoot && !strings.HasPrefix(resolved, realRoot+string(filepath.Separator)) {
			t.Fatalf("accepted path escapes via symlink: %q -> %q (%q)", in, abs, resolved)
		}
	})
}
