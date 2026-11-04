package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madeofpendletonwool/yana/internal/drawing"
	"github.com/madeofpendletonwool/yana/internal/frontmatter"
)

const (
	lzInput    = `N4IgLgngDgpiBcIYA8DGBDANgSwCYCd0B3EAGhADcZ8BnbAewDsEAmcm+gV31TkQAswYKDXgB6MQHNsYfpwBGAOlT0AtmIBeNCtlQbs6RmPry6uA4wC0KDDgLFLUTJ2nNyMTDFUxGYGggBtUDwEcABGMnBoPnAUMEiwOND6TABDgAIAgIBZTkZcegBdQsj6fGxXLAAVJMQUjKzc/KKS8mwaABEPGETcBAAzLBoYchxGAGsERk5MTABfUmC+xHwI8khYUPwYVDBDSU9IscnELIB5TjBCdMZ6PYAfKnxiyPauz16BoZg5wvJ0KBQADKe0SCFAOhgRAAQuhUONJPguPkAMIpMqhADE/RxuMiiLwQOwGj401mCxA/Wwnn88GAczmQA==`
	lzExpected = `{"type":"excalidraw","version":2,"source":"https://github.com/zsviczian/obsidian-excalidraw-plugin","elements":[{"id":"t1","type":"text","text":"olá [[Mundo]]","originalText":"olá [[Mundo]]","isDeleted":false,"link":null},{"id":"r1","type":"rectangle","link":"[[Outra nota|ver]]","isDeleted":false}],"appState":{"viewBackgroundColor":"#ffffff","gridSize":null},"files":{}}`
)

func obsidianFile(fence, payload string) string {
	return "---\nid: 01ABC\nexcalidraw-plugin: parsed\ntags: [excalidraw]\n---\n" +
		"==⚠ Switch to EXCALIDRAW VIEW ⚠==\n\n# Text Elements\nolá [[Mundo]] ^t1\n\n%%\n# Drawing\n```" +
		fence + "\n" + payload + "\n```\n%%\n"
}

func TestConvertDrawings(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"s/A.excalidraw.md": obsidianFile("compressed-json", lzInput),
		"s/B.md":            obsidianFile("json", lzExpected),
		"s/C.md":            "---\nid: 01XYZ\n---\nhello\n",
		"s/D.excalidraw.md": obsidianFile("compressed-json", "!!!!"),
		"s/_assets/E.md":    obsidianFile("json", lzExpected),
		"s/.git/F.md":       obsidianFile("json", lzExpected),
	}
	for rel, c := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	unchanged := func() {
		t.Helper()
		for rel, c := range files {
			if read(rel) != c {
				t.Errorf("%s changed", rel)
			}
		}
	}

	var out bytes.Buffer
	conv, failed, err := convertDrawings(root, false, &out)
	if err != nil || conv != 2 || failed != 1 {
		t.Fatalf("dry run = %d, %d, %v", conv, failed, err)
	}
	for _, want := range []string{
		"ok   s/A.excalidraw.md (2 elements)\n",
		"ok   s/B.md (2 elements)\n",
		"     rename s/B.md → s/B.excalidraw.md\n",
		"FAIL s/D.excalidraw.md: ",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "rename s/A") || strings.Contains(out.String(), "E.md") || strings.Contains(out.String(), "F.md") {
		t.Errorf("unexpected output:\n%s", out.String())
	}
	unchanged()

	out.Reset()
	conv, failed, err = convertDrawings(root, true, &out)
	if err != nil || conv != 2 || failed != 1 {
		t.Fatalf("apply = %d, %d, %v", conv, failed, err)
	}
	for _, rel := range []string{"s/A.excalidraw.md", "s/B.md"} {
		d := frontmatter.Parse([]byte(read(rel)))
		s, _, err := drawing.Parse(d.Body)
		if err != nil || len(s.Elements) != 2 || !strings.Contains(string(d.Head), "id: 01ABC") {
			t.Errorf("%s not converted: %v\n%s", rel, err, read(rel))
		}
		delete(files, rel)
	}
	unchanged()

	// A second run finds nothing left to convert.
	conv, failed, _ = convertDrawings(root, true, &out)
	if conv != 0 || failed != 1 {
		t.Errorf("rerun = %d, %d", conv, failed)
	}
}
