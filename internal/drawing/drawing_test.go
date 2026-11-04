package drawing

import (
	"errors"
	"reflect"
	"testing"
)

func TestIs(t *testing.T) {
	for rel, want := range map[string]bool{
		"a/B.excalidraw.md": true,
		"x.EXCALIDRAW.MD":   true,
		"a.md":              false,
		"a.excalidraw":      false,
	} {
		if got := Is(rel); got != want {
			t.Errorf("Is(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestSplitExt(t *testing.T) {
	for _, c := range []struct{ name, stem, ext string }{
		{"X.excalidraw.md", "X", ".excalidraw.md"},
		{"X.conflict-20261104-120000.excalidraw.md", "X.conflict-20261104-120000", ".excalidraw.md"},
		{"n.md", "n", ".md"},
		{"p.html", "p", ".html"},
	} {
		stem, ext := SplitExt(c.name)
		if stem != c.stem || ext != c.ext {
			t.Errorf("SplitExt(%q) = (%q, %q), want (%q, %q)", c.name, stem, ext, c.stem, c.ext)
		}
	}
}

func TestParseRoundTrip(t *testing.T) {
	s, raw, err := Parse(Encode([]byte(EmptyScene)))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Elements) != 0 {
		t.Errorf("elements = %d, want 0", len(s.Elements))
	}
	if string(raw) != EmptyScene {
		t.Errorf("raw = %q, want %q", raw, EmptyScene)
	}
}

func TestParseRejects(t *testing.T) {
	if _, _, err := Parse([]byte("# hi\n")); !errors.Is(err, ErrNoScene) {
		t.Errorf("no block: err = %v, want ErrNoScene", err)
	}
	for _, body := range []string{
		"```excalidraw\n{\"type\":\"other\"}\n```\n",
		"```excalidraw\n{bad\n```\n",
	} {
		if _, _, err := Parse([]byte(body)); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", body)
		}
	}
}

func TestTextAndLinks(t *testing.T) {
	body := "intro\n```excalidraw  \n" + `{"type":"excalidraw","elements":[
{"type":"text","text":"a","originalText":"olá [[Mundo]]","link":null},
{"type":"rectangle","link":"[[Outra nota|ver]]"},
{"type":"ellipse","link":"https://x.y"},
{"type":"diamond","link":"[[Outra nota]]"},
{"type":"text","text":"gone","isDeleted":true}]}` + "\n```\n"
	s, _, err := Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Text(); got != "olá [[Mundo]]" {
		t.Errorf("Text() = %q", got)
	}
	if got, want := s.Links(), []string{"Outra nota"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Links() = %q, want %q", got, want)
	}
}
