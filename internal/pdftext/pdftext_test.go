package pdftext

import (
	"strings"
	"testing"

	"github.com/madeofpendletonwool/yana/internal/testpdf"
)

func TestExtract(t *testing.T) {
	data := testpdf.Build(
		"Kettle manual page one",
		"Descale quarterly with citric acid",
		"Warranty void if opened",
	)
	pages, text, err := Extract(strings.NewReader(string(data)), int64(len(data)))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if pages != 3 {
		t.Fatalf("pages = %d, want 3", pages)
	}
	for _, want := range []string{"Kettle manual page one", "Descale quarterly", "Warranty void"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text %q missing %q", text, want)
		}
	}
}

func TestExtractNoTextLayer(t *testing.T) {
	// A PDF whose pages hold no text operators at all: page count still
	// reads, text comes back empty, and that is not an error.
	data := testpdf.Build("")
	pages, text, err := Extract(strings.NewReader(string(data)), int64(len(data)))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if pages != 1 {
		t.Fatalf("pages = %d, want 1", pages)
	}
	if strings.TrimSpace(text) != "" {
		t.Fatalf("text = %q, want empty", text)
	}
}

func TestExtractNotAPDF(t *testing.T) {
	data := []byte("this is a text file with a .pdf name")
	if _, _, err := Extract(strings.NewReader(string(data)), int64(len(data))); err == nil {
		t.Fatal("expected an error for a non-pdf file")
	}
}
