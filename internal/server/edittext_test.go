package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A remote editor fetches a note, an append lands while it is open, and
// its save from the old base keeps the append. Saving the same text again
// changes nothing; viewers, non-markdown notes and bad bases are refused.
func TestNoteTextMerge(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)
	url := "/api/notes/" + w.homeID + "/text"

	code, got := doGet(t, f.ts, url, w.samHdr)
	if code != 200 || got["text"] != "the raspberry jam recipe\n" || got["role"] != "editor" {
		t.Fatalf("get: %d %v", code, got)
	}
	base := got["base"].(string)

	if code, body := doPost(t, f.ts, "POST", "/api/notes/"+w.homeID+"/append", w.ownerHdr, map[string]string{"content": "- appended meanwhile"}); code != 200 {
		t.Fatalf("append: %d %v", code, body)
	}
	code, got = doPost(t, f.ts, "PUT", url, w.samHdr, map[string]string{"base": base, "text": "the strawberry jam recipe\n"})
	if code != 200 {
		t.Fatalf("put: %d %v", code, got)
	}
	if want := "the strawberry jam recipe\n- appended meanwhile\n"; got["text"] != want {
		t.Fatalf("merged %q, want %q", got["text"], want)
	}
	if err := f.rec.Flush(context.Background(), w.homeID); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(f.dir, "home", "home-secret.md"))
	if !strings.Contains(string(raw), "strawberry") || !strings.Contains(string(raw), "- appended meanwhile") {
		t.Fatalf("file:\n%s", raw)
	}

	// The same text from the base the save returned is no update.
	before, _ := f.rec.State(context.Background(), w.homeID)
	if code, body := doPost(t, f.ts, "PUT", url, w.samHdr, map[string]string{"base": got["base"].(string), "text": "the strawberry jam recipe\n"}); code != 200 {
		t.Fatalf("no-op put: %d %v", code, body)
	}
	after, _ := f.rec.State(context.Background(), w.homeID)
	if string(before) != string(after) {
		t.Fatal("a save with no change wrote to the document")
	}

	if code, _ := doPost(t, f.ts, "PUT", url, w.eveHdr, map[string]string{"base": base, "text": "vandalism"}); code != 403 {
		t.Fatalf("viewer put: %d", code)
	}
	if code, _ := doGet(t, f.ts, url, w.eveHdr); code != 200 {
		t.Fatalf("viewer get: %d", code)
	}
	for _, bad := range []string{"Z2FyYmFnZQ==", "", "not base64!"} {
		if code, _ := doPost(t, f.ts, "PUT", url, w.samHdr, map[string]string{"base": bad, "text": "x"}); code != 400 {
			t.Fatalf("base %q: %d", bad, code)
		}
	}
	page := f.writeNote(t, "home/page.html", "<p>hi</p>\n")
	if code, _ := doGet(t, f.ts, "/api/notes/"+page+"/text", w.samHdr); code != 400 {
		t.Fatalf("html get: %d", code)
	}
	if code, _ := doPost(t, f.ts, "PUT", "/api/notes/"+page+"/text", w.samHdr, map[string]string{"base": base, "text": "x"}); code != 400 {
		t.Fatalf("html put: %d", code)
	}
}
