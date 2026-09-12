package search

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRipgrep(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not installed")
	}
	dir := t.TempDir()
	write := func(rel, s string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	write("home/a.md", "alpha\nTODO: buy milk\n")
	write("home/b.txt", "TODO: not a note\n")
	write("work/c.md", "TODO: ship it\n")
	write(".sync/x.md", "TODO: hidden\n")
	write("home/.dot.md", "TODO: dotfile\n")

	r := NewRipgrep(dir, true, 5*time.Second)
	if !r.Available() {
		t.Fatal("rg should be available")
	}
	got, err := r.Search(context.Background(), `TODO:\s+\w+`, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("matches: %+v", got)
	}
	paths := map[string]bool{}
	for _, m := range got {
		paths[m.Path] = true
	}
	if !paths["home/a.md"] || !paths["work/c.md"] {
		t.Fatalf("paths: %+v", got)
	}
	got, _ = r.Search(context.Background(), "TODO", "work", 10)
	if len(got) != 1 || got[0].Path != "work/c.md" || got[0].Line != 1 {
		t.Fatalf("space-scoped: %+v", got)
	}
	got, err = r.Search(context.Background(), "nomatch", "", 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("no match: %v %+v", err, got)
	}
	_, err = r.Search(context.Background(), "(unclosed", "", 10)
	if !errors.Is(err, ErrBadPattern) {
		t.Fatalf("bad pattern: %v", err)
	}
	got, _ = r.Search(context.Background(), "TODO", "", 1)
	if len(got) != 1 {
		t.Fatalf("limit: %+v", got)
	}
	off := NewRipgrep(dir, false, time.Second)
	if _, err := off.Search(context.Background(), "x", "", 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disabled: %v", err)
	}
}
