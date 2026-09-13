package ydoc

import (
	"math/rand"
	"testing"
)

func TestSetTextMergesWithConcurrentEdit(t *testing.T) {
	a := New()
	b := New()
	u := a.SetText("hello world\n", "test")
	if _, err := b.Apply(u, "remote"); err != nil {
		t.Fatal(err)
	}
	// a types at the end while b (the file) appends a different line.
	ua := a.Insert(utf16Len("hello world\n"), "from a\n", "user:a")
	ub := b.SetText("hello world\nfrom file\n", "filesystem")
	if _, err := a.Apply(ub, "remote"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Apply(ua, "remote"); err != nil {
		t.Fatal(err)
	}
	if a.Text() != b.Text() {
		t.Fatalf("diverged: %q vs %q", a.Text(), b.Text())
	}
	got := a.Text()
	if got != "hello world\nfrom a\nfrom file\n" && got != "hello world\nfrom file\nfrom a\n" {
		t.Fatalf("unexpected merge: %q", got)
	}
}

func TestSetTextNoChangeReturnsNil(t *testing.T) {
	d := New()
	d.SetText("x", "t")
	if u := d.SetText("x", "t"); u != nil {
		t.Fatalf("expected nil update, got %d bytes", len(u))
	}
}

func TestApplyKnownUpdateReturnsNil(t *testing.T) {
	a := New()
	u := a.SetText("abc", "t")
	b := New()
	if _, err := b.Apply(u, "r"); err != nil {
		t.Fatal(err)
	}
	again, err := b.Apply(u, "r")
	if err != nil {
		t.Fatal(err)
	}
	if again != nil {
		t.Fatalf("re-applying a known update should emit nothing, got %d bytes", len(again))
	}
}

func TestLoadRoundTrip(t *testing.T) {
	a := New()
	a.SetText("héllo 🙂 world", "t")
	b, err := Load(a.State())
	if err != nil {
		t.Fatal(err)
	}
	if b.Text() != a.Text() {
		t.Fatalf("round trip: %q", b.Text())
	}
	d, err := b.Diff(a.StateVector())
	if err != nil {
		t.Fatal(err)
	}
	if !isEmptyUpdate(d) {
		t.Fatalf("expected empty diff, got %d bytes", len(d))
	}
}

func TestInvalidUTF8IsReplaced(t *testing.T) {
	d := New()
	d.SetText("ok\xffbad", "t")
	if d.Text() != "ok�bad" {
		t.Fatalf("got %q", d.Text())
	}
}

func TestRandomConvergence(t *testing.T) {
	words := []string{"a", "bb", "ε", "🙂", "\n", " ", "line"}
	for seed := int64(0); seed < 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		a, b := New(), New()
		for i := 0; i < 20; i++ {
			var u []byte
			src, dst := a, b
			if rng.Intn(2) == 0 {
				src, dst = b, a
			}
			cur := []rune(src.Text())
			if rng.Intn(2) == 0 {
				cut := 0
				if len(cur) > 0 {
					cut = rng.Intn(len(cur) + 1)
				}
				u = src.SetText(string(cur[:cut])+words[rng.Intn(len(words))]+string(cur[cut:]), "fs")
			} else if len(cur) > 0 {
				start := rng.Intn(len(cur))
				end := start + rng.Intn(len(cur)-start) + 1
				u = src.SetText(string(cur[:start])+string(cur[end:]), "fs")
			}
			if u != nil {
				if _, err := dst.Apply(u, "r"); err != nil {
					t.Fatal(err)
				}
			}
		}
		if a.Text() != b.Text() {
			t.Fatalf("seed %d diverged: %q vs %q", seed, a.Text(), b.Text())
		}
	}
}
