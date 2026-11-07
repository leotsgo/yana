package git_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leotsgo/yana/internal/git"
)

func sealingLayer(t *testing.T) *git.Layer {
	t.Helper()
	dir := t.TempDir()
	opts := testOptions()
	opts.SecretPath = filepath.Join(dir, ".sync", "git_secret")
	l := git.New(dir, opts, nil)
	if err := l.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestNewKeyRoundTrip(t *testing.T) {
	l := sealingLayer(t)
	pub, sealed, err := l.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") || !strings.HasSuffix(pub, " yana") {
		t.Fatalf("public key = %q", pub)
	}
	if strings.Contains(sealed, "PRIVATE KEY") {
		t.Fatal("sealed key holds the PEM header")
	}
	raw, pub2, err := l.OpenKey(sealed)
	if err != nil || pub2 != pub || len(raw) == 0 {
		t.Fatalf("OpenKey = (%d bytes, %q, %v), want the same public key", len(raw), pub2, err)
	}
}

func TestOpenKeyRejectsForeign(t *testing.T) {
	_, sealed, err := sealingLayer(t).NewKey()
	if err != nil {
		t.Fatal(err)
	}
	l := sealingLayer(t)
	for _, in := range []string{sealed, "garbage"} {
		if _, _, err := l.OpenKey(in); !errors.Is(err, git.ErrForeignKey) {
			t.Errorf("OpenKey(%.10q) err = %v, want ErrForeignKey", in, err)
		}
	}
}
