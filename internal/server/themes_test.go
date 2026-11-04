package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestThemesFile(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)
	theme := []map[string]any{{"name": "Forest", "mode": "dark", "colors": map[string]any{"bg": "#14201a", "ink": "#d8e6d0", "accent": "#e0b24a"}}}

	if code, _ := doPost(t, f.ts, "PUT", "/api/themes", w.samHdr, theme); code != 403 {
		t.Fatalf("non-owner put %d", code)
	}
	if code, _ := doPost(t, f.ts, "PUT", "/api/themes", w.ownerHdr, theme); code != 200 {
		t.Fatalf("owner put %d", code)
	}
	raw, err := os.ReadFile(filepath.Join(f.root.Dir(), ".yana", "themes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []map[string]any
	if err := json.Unmarshal(raw, &onDisk); err != nil || len(onDisk) != 1 || onDisk[0]["name"] != "Forest" {
		t.Fatalf("file = %s (%v)", raw, err)
	}
	if code, _ := doGet(t, f.ts, "/api/themes", w.samHdr); code != 200 {
		t.Fatalf("get %d", code)
	}
	if code, _ := doGet(t, f.ts, "/api/themes", ""); code != 401 {
		t.Fatalf("anonymous get %d", code)
	}
	if code, _ := doPost(t, f.ts, "PUT", "/api/themes", w.ownerHdr, map[string]any{"name": "x"}); code != 400 {
		t.Fatalf("object body %d", code)
	}
}
