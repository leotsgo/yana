package frontmatter

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 12, 14, 2, 11, 0, time.UTC)

func TestParseBlock(t *testing.T) {
	src := "---\nid: 01ABC\ncreated: 2026-09-12T14:02:11Z\norder: 3\ntrusted: true\ntemplate: daily\ncustom: keep me\n# a comment\n---\n# Title\n\nbody\n"
	d := Parse([]byte(src))
	if !d.HasBlock {
		t.Fatal("block not detected")
	}
	if d.Meta.ID != "01ABC" || !d.Meta.Created.Equal(now) || d.Meta.Order == nil || *d.Meta.Order != 3 || !d.Meta.Trusted || d.Meta.Template != "daily" {
		t.Fatalf("meta: %+v", d.Meta)
	}
	if string(d.Body) != "# Title\n\nbody\n" {
		t.Fatalf("body: %q", d.Body)
	}
	if len(d.Meta.Raw) != 6 || d.Meta.Raw[5][0] != "custom" || d.Meta.Raw[5][1] != "keep me" {
		t.Fatalf("raw: %v", d.Meta.Raw)
	}
}

func TestParseNoBlock(t *testing.T) {
	for _, src := range []string{"", "# Title\n", "--- not a block\n", "---\nunterminated\n", "\n---\nid: x\n---\n"} {
		d := Parse([]byte(src))
		if d.HasBlock {
			t.Errorf("%q: block wrongly detected", src)
		}
		if string(d.Body) != src {
			t.Errorf("%q: body altered to %q", src, d.Body)
		}
	}
}

func TestEnsureIDPrependsBlock(t *testing.T) {
	out, meta, changed := EnsureID([]byte("# Hello\n"), "01NEW", now)
	if !changed || meta.ID != "01NEW" || !meta.Created.Equal(now) {
		t.Fatalf("changed=%v meta=%+v", changed, meta)
	}
	want := "---\nid: 01NEW\ncreated: 2026-09-12T14:02:11Z\n---\n# Hello\n"
	if string(out) != want {
		t.Fatalf("got %q want %q", out, want)
	}
	// Idempotent.
	out2, _, changed2 := EnsureID(out, "01OTHER", now.Add(time.Hour))
	if changed2 || string(out2) != want {
		t.Fatal("second EnsureID should be a no-op")
	}
}

func TestEnsureIDPreservesExistingKeysByteForByte(t *testing.T) {
	src := "---\ntitle:   \"Spaced out\"   \ntags: [a, b]\n\n# comment\ncreated: 2020-01-01\n---\nbody\n"
	out, meta, changed := EnsureID([]byte(src), "01NEW", now)
	if !changed {
		t.Fatal("expected change")
	}
	if meta.Created.Year() != 2020 {
		t.Fatalf("existing created must be kept: %v", meta.Created)
	}
	if !strings.HasPrefix(string(out), "---\nid: 01NEW\ntitle:   \"Spaced out\"   \n") {
		t.Fatalf("id not inserted at top / formatting altered: %q", out)
	}
	if !strings.HasSuffix(string(out), "created: 2020-01-01\n---\nbody\n") {
		t.Fatalf("tail altered: %q", out)
	}
	if strings.Count(string(out), "created:") != 1 {
		t.Fatal("created must not be duplicated")
	}
	// Removing the inserted line must give back the exact original.
	restored := strings.Replace(string(out), "id: 01NEW\n", "", 1)
	if restored != src {
		t.Fatalf("round trip lost bytes:\n%q\n%q", restored, src)
	}
}

func TestEnsureIDCRLF(t *testing.T) {
	src := "---\r\ntitle: x\r\n---\r\nbody\r\n"
	out, _, _ := EnsureID([]byte(src), "01NEW", now)
	if !strings.HasPrefix(string(out), "---\r\nid: 01NEW\r\ncreated: 2026-09-12T14:02:11Z\r\ntitle: x\r\n---\r\n") {
		t.Fatalf("CRLF not preserved: %q", out)
	}
}

func TestEnsureIDEmptyFile(t *testing.T) {
	out, _, _ := EnsureID(nil, "01NEW", now)
	if string(out) != "---\nid: 01NEW\ncreated: 2026-09-12T14:02:11Z\n---\n" {
		t.Fatalf("%q", out)
	}
}

func TestReplaceID(t *testing.T) {
	src := "---\ntitle: x\nid: 01OLD\ncreated: 2020-01-01\n---\nbody\n"
	out, ok := ReplaceID([]byte(src), "01NEW")
	if !ok || string(out) != "---\ntitle: x\nid: 01NEW\ncreated: 2020-01-01\n---\nbody\n" {
		t.Fatalf("ok=%v %q", ok, out)
	}
	if _, ok := ReplaceID([]byte("no block"), "x"); ok {
		t.Fatal("should not replace without a block")
	}
	crlf := "---\r\nid: 01OLD\r\n---\r\n"
	out, ok = ReplaceID([]byte(crlf), "01NEW")
	if !ok || string(out) != "---\r\nid: 01NEW\r\n---\r\n" {
		t.Fatalf("crlf: %q", out)
	}
}
