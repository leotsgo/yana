package drawing

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func tmpFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "d.excalidraw.md")
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRewriteWritesEdit(t *testing.T) {
	p := tmpFile(t, "a", 0o600)
	prev, next, err := Rewrite(p, func([]byte) ([]byte, error) { return []byte("b"), nil })
	if err != nil || string(prev) != "a" || string(next) != "b" {
		t.Fatalf("got %q %q %v", prev, next, err)
	}
	got, _ := os.ReadFile(p)
	fi, _ := os.Stat(p)
	if string(got) != "b" || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file %q mode %v", got, fi.Mode().Perm())
	}
}

func TestRewriteNilWritesNothing(t *testing.T) {
	p := tmpFile(t, "a", 0o644)
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(p, old, old)
	if _, _, err := Rewrite(p, func([]byte) ([]byte, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	fi, _ := os.Stat(p)
	if string(got) != "a" || !fi.ModTime().Equal(old.Truncate(time.Second)) && fi.ModTime().After(old.Add(time.Minute)) {
		t.Fatalf("file touched: %q %v", got, fi.ModTime())
	}
}

func TestRewriteErrorWritesNothing(t *testing.T) {
	p := tmpFile(t, "a", 0o644)
	boom := errors.New("boom")
	if _, _, err := Rewrite(p, func([]byte) ([]byte, error) { return []byte("b"), boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(p); string(got) != "a" {
		t.Fatalf("file %q", got)
	}
}

func TestRewriteSerializes(t *testing.T) {
	p := tmpFile(t, "", 0o644)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := Rewrite(p, func(f []byte) ([]byte, error) { return append(f, 'x'), nil })
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got, _ := os.ReadFile(p); len(got) != 20 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestHash(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	if Hash([]byte("x")) != hex.EncodeToString(sum[:]) {
		t.Fatal("hash mismatch")
	}
}
