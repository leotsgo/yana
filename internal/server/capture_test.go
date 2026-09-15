package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (e *linksEnv) put(t *testing.T, path string, body []byte, out any) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, e.ts.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "image/png")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && resp.StatusCode < 500 {
			t.Fatalf("%s: decode: %v", path, err)
		}
	}
	return resp.StatusCode
}

func TestUploadAsset(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	var res struct {
		Path string `json:"path"`
		Name string `json:"name"`
		Size int    `json:"size"`
		URL  string `json:"url"`
	}
	if code := e.put(t, "/api/files/main/docs/_assets/shot.png", []byte("PNG1"), &res); code != 201 {
		t.Fatalf("upload = %d", code)
	}
	if res.Path != "main/docs/_assets/shot.png" || res.Name != "shot.png" || res.Size != 4 {
		t.Fatalf("upload response = %+v", res)
	}
	raw, err := os.ReadFile(filepath.Join(e.dir, "main", "docs", "_assets", "shot.png"))
	if err != nil || string(raw) != "PNG1" {
		t.Fatalf("file on disk = %q, %v", raw, err)
	}
	// The endpoint that serves assets answers with what was written.
	resp, err := http.Get(e.ts.URL + res.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("serve uploaded asset = %d", resp.StatusCode)
	}

	// Same name again: the second upload gets a suffix, the first file is untouched.
	var again struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if code := e.put(t, "/api/files/main/docs/_assets/shot.png", []byte("PNG2"), &again); code != 201 {
		t.Fatalf("second upload = %d", code)
	}
	if again.Path != "main/docs/_assets/shot-2.png" {
		t.Fatalf("second upload path = %q", again.Path)
	}
	raw, _ = os.ReadFile(filepath.Join(e.dir, "main", "docs", "_assets", "shot.png"))
	if string(raw) != "PNG1" {
		t.Fatalf("first upload was overwritten: %q", raw)
	}
	raw, _ = os.ReadFile(filepath.Join(e.dir, "main", "docs", "_assets", "shot-2.png"))
	if string(raw) != "PNG2" {
		t.Fatalf("second upload content = %q", raw)
	}
}

func TestUploadAssetRejects(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	cases := []struct {
		path string
		body string
		want int
	}{
		{"/api/files/main/docs/shot.png", "PNG", 400},            // not under _assets
		{"/api/files/main/_assets/note.md", "# hi", 400},         // notes are not uploads
		{"/api/files/main/_assets/../../escape.png", "PNG", 400}, // traversal
		{"/api/files/main/_assets/empty.png", "", 400},           // empty body
	}
	for _, c := range cases {
		if code := e.put(t, c.path, []byte(c.body), nil); code != c.want {
			t.Errorf("PUT %s = %d, want %d", c.path, code, c.want)
		}
	}
	if _, err := os.Stat(filepath.Join(e.dir, "escape.png")); err == nil {
		t.Fatal("traversal wrote a file")
	}
}

func TestUploadAssetTooLarge(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	big := bytes.Repeat([]byte("x"), int(e.srv.Root.Limits().MaxAssetSize)+1)
	if code := e.put(t, "/api/files/main/_assets/big.bin", big, nil); code != 413 {
		t.Fatalf("oversize upload = %d", code)
	}
}

func TestUploadAssetDenied(t *testing.T) {
	e := newLinksEnv(t, false, func(r *http.Request, space string) error {
		if space == "main" {
			return context.Canceled
		}
		return nil
	})
	if code := e.put(t, "/api/files/main/_assets/x.png", []byte("PNG"), nil); code != 403 {
		t.Fatalf("denied upload = %d", code)
	}
	if code := e.put(t, "/api/files/other/_assets/x.png", []byte("PNG"), nil); code != 201 {
		t.Fatalf("allowed upload = %d", code)
	}
}

func TestDailyNote(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	os.MkdirAll(filepath.Join(e.dir, "main", "templates"), 0o755)
	os.WriteFile(filepath.Join(e.dir, "main", "templates", "daily.md"),
		[]byte("---\nid: 01JQ8X4K2M9P7R3T5V6W8Y0Z1A\n---\n# {date}\n\n## Today\n\n- \n"), 0o644)

	var first struct {
		ID      string `json:"id"`
		Path    string `json:"path"`
		Created bool   `json:"created"`
	}
	if code := e.post(t, "/api/notes/daily", map[string]string{"space": "main", "date": "2026-09-15"}, &first); code != 201 {
		t.Fatalf("daily create = %d", code)
	}
	if first.Path != "main/journal/2026/09/2026-09-15.md" || !first.Created || first.ID == "" {
		t.Fatalf("daily create response = %+v", first)
	}
	raw, err := os.ReadFile(filepath.Join(e.dir, filepath.FromSlash(first.Path)))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "# 2026-09-15\n\n## Today") {
		t.Fatalf("template not expanded: %q", body)
	}
	if strings.Contains(body, "01JQ8X4K2M9P7R3T5V6W8Y0Z1A") {
		t.Fatalf("template id leaked into the daily note: %q", body)
	}
	if !strings.Contains(body, "id: "+first.ID) {
		t.Fatalf("daily note has no id of its own: %q", body)
	}

	var second struct {
		ID      string `json:"id"`
		Created bool   `json:"created"`
	}
	if code := e.post(t, "/api/notes/daily", map[string]string{"space": "main", "date": "2026-09-15"}, &second); code != 200 {
		t.Fatalf("daily again = %d", code)
	}
	if second.Created || second.ID != first.ID {
		t.Fatalf("second call should return the same note: %+v vs %+v", second, first)
	}

	if code := e.post(t, "/api/notes/daily", map[string]string{"space": "main", "date": "15/09/2026"}, nil); code != 400 {
		t.Fatalf("bad date = %d", code)
	}
	if code := e.post(t, "/api/notes/daily", map[string]string{"space": "../x", "date": "2026-09-15"}, nil); code != 400 {
		t.Fatalf("bad space = %d", code)
	}
}

func TestDailyNoteWithoutTemplate(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	var res struct {
		Path string `json:"path"`
	}
	if code := e.post(t, "/api/notes/daily", map[string]string{"space": "other", "date": "2026-01-02"}, &res); code != 201 {
		t.Fatalf("daily create = %d", code)
	}
	raw, _ := os.ReadFile(filepath.Join(e.dir, filepath.FromSlash(res.Path)))
	if !strings.HasSuffix(string(raw), "# 2026-01-02\n\n") {
		t.Fatalf("fallback seed = %q", raw)
	}
}

func TestRenderEndpoint(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	var res struct {
		HTML string `json:"html"`
	}
	md := "---\nid: x\n---\n# Title\n\nSee [[hello]] and `code`.\n"
	if code := e.post(t, "/api/render", map[string]string{"markdown": md}, &res); code != 200 {
		t.Fatalf("render = %d", code)
	}
	if !strings.Contains(res.HTML, "<h1") || !strings.Contains(res.HTML, "wikilink") || strings.Contains(res.HTML, "id: x") {
		t.Fatalf("render html = %q", res.HTML)
	}
}

func TestStatusCarriesDailyConfig(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	var st struct {
		Daily struct {
			Pattern  string `json:"pattern"`
			Template string `json:"template"`
		} `json:"daily"`
	}
	e.get(t, "/api/status", &st)
	if st.Daily.Pattern != DefaultDaily.Pattern {
		t.Fatalf("status daily = %+v", st.Daily)
	}
}
