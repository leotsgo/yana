package drawing

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
)

const (
	lzInput    = `N4IgLgngDgpiBcIYA8DGBDANgSwCYCd0B3EAGhADcZ8BnbAewDsEAmcm+gV31TkQAswYKDXgB6MQHNsYfpwBGAOlT0AtmIBeNCtlQbs6RmPry6uA4wC0KDDgLFLUTJ2nNyMTDFUxGYGggBtUDwEcABGMnBoPnAUMEiwOND6TABDgAIAgIBZTkZcegBdQsj6fGxXLAAVJMQUjKzc/KKS8mwaABEPGETcBAAzLBoYchxGAGsERk5MTABfUmC+xHwI8khYUPwYVDBDSU9IscnELIB5TjBCdMZ6PYAfKnxiyPauz16BoZg5wvJ0KBQADKe0SCFAOhgRAAQuhUONJPguPkAMIpMqhADE/RxuMiiLwQOwGj401mCxA/Wwnn88GAczmQA==`
	lzExpected = `{"type":"excalidraw","version":2,"source":"https://github.com/zsviczian/obsidian-excalidraw-plugin","elements":[{"id":"t1","type":"text","text":"olá [[Mundo]]","originalText":"olá [[Mundo]]","isDeleted":false,"link":null},{"id":"r1","type":"rectangle","link":"[[Outra nota|ver]]","isDeleted":false}],"appState":{"viewBackgroundColor":"#ffffff","gridSize":null},"files":{}}`
)

func obsidianFile(fence, payload string) []byte {
	return []byte("---\nid: 01ABC\nexcalidraw-plugin: parsed\ntags: [excalidraw]\n---\n" +
		"==⚠ Switch to EXCALIDRAW VIEW ⚠==\n\n# Text Elements\nolá [[Mundo]] ^t1\n\n%%\n# Drawing\n```" +
		fence + "\n" + payload + "\n```\n%%\n")
}

func TestDecompressFromBase64(t *testing.T) {
	got, err := decompressFromBase64(lzInput)
	if err != nil {
		t.Fatal(err)
	}
	if got != lzExpected {
		t.Errorf("got %q", got)
	}
}

func checkConverted(t *testing.T, out []byte) {
	t.Helper()
	d := frontmatter.Parse(out)
	head := string(d.Head)
	if !strings.Contains(head, "id: 01ABC") || !strings.Contains(head, "tags:") || strings.Contains(head, "excalidraw-plugin") {
		t.Errorf("head = %q", head)
	}
	s, _, err := Parse(d.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Elements) != 2 || !reflect.DeepEqual(s.Links(), []string{"Outra nota"}) {
		t.Errorf("scene = %+v links = %v", s, s.Links())
	}
}

func TestFromObsidianCompressed(t *testing.T) {
	var lines []string
	for in := lzInput; in != ""; {
		n := min(64, len(in))
		lines = append(lines, in[:n])
		in = in[n:]
	}
	in := obsidianFile("compressed-json", strings.Join(lines, "\n"))
	if !IsObsidian(in) {
		t.Fatal("IsObsidian = false")
	}
	out, err := FromObsidian(in)
	if err != nil {
		t.Fatal(err)
	}
	checkConverted(t, out)
}

func TestFromObsidianJSON(t *testing.T) {
	out, err := FromObsidian(obsidianFile("json", lzExpected))
	if err != nil {
		t.Fatal(err)
	}
	checkConverted(t, out)
	if !bytes.HasSuffix(out, Encode([]byte(lzExpected))) {
		t.Errorf("body is not the encoded scene: %q", out)
	}
}

func TestFromObsidianRejects(t *testing.T) {
	note := []byte("---\nid: 01ABC\n---\nhello\n")
	if IsObsidian(note) {
		t.Error("IsObsidian(normal note) = true")
	}
	if _, err := FromObsidian(obsidianFile("text", "x")); err == nil {
		t.Error("no drawing block: want error")
	}
	if _, err := FromObsidian(obsidianFile("compressed-json", "!!!!")); err == nil {
		t.Error("garbage compressed block: want error")
	}
	if _, err := FromObsidian(obsidianFile("json", `not json`)); err == nil {
		t.Error("non-JSON scene: want error")
	}
	if _, err := FromObsidian(obsidianFile("json", `{"type":"other"}`)); err == nil {
		t.Error("invalid scene: want error")
	}
}

func TestDecompressFromBase64Wide(t *testing.T) {
	const in = `N4IgLgngDgpiBcIYA8DGBDANgSwCYCd0B3EAGhADcZ8BnbAewDsEAmcmTGAWxkbBoQBtUHgTgWZcNDiIwKMJLnIFidAAJASYRqARmsC8G4AC9tYFPTQDTmawCgEa+gGtJ9fNgDm2RlgAq8seq27DJ8ytbSWwaABEOGDlcBAAzLBoYchxGO3hGAFdMTABfAF1ydCgoAGUwdDkEYBzyWOxOAXhqnKA`
	const want = `{"type":"excalidraw","version":2,"elements":[{"id":"t2","type":"text","text":"a → b 🚀 日本 — ok","originalText":"a → b 🚀 日本 — ok","isDeleted":false,"link":null}],"appState":{},"files":{}}`
	got, err := decompressFromBase64(in)
	if err != nil || got != want {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestDecompressFromBase64Cap(t *testing.T) {
	defer func(n int) { maxDecodedUnits = n }(maxDecodedUnits)
	maxDecodedUnits = 10
	if _, err := decompressFromBase64(lzInput); err == nil {
		t.Error("want error past the cap")
	}
}
