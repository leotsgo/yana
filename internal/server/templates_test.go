package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tplEnv is a linksEnv with one template note in place.
func tplEnv(t *testing.T, name, content string) (*linksEnv, string) {
	t.Helper()
	e := newLinksEnv(t, false, nil)
	p := filepath.Join(e.dir, filepath.FromSlash("main/templates/"+name))
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(content), 0o644)
	old := time.Now().Add(-time.Minute)
	os.Chtimes(p, old, old)
	if _, err := e.sc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	n, err := e.db.GetNoteByPath(context.Background(), "main/templates/"+name)
	if err != nil {
		t.Fatal(err)
	}
	return e, n.ID
}

// Expanding a template substitutes the variables, reports the prompts it
// asks, says where the caret lands, and suggests a title from its name.
func TestTemplateExpand(t *testing.T) {
	e, id := tplEnv(t, "Meeting {{date}}.md",
		"---\nid: 01TEMPLATE0000000000000000\n---\n# {{title}}\n\n{{date}} with {{prompt:Attendees}} in {{folder}}.\n\n{{cursor}}Agenda:\n")
	var res struct {
		Body    string   `json:"body"`
		Cursor  int      `json:"cursor"`
		Prompts []string `json:"prompts"`
		Suggest string   `json:"suggest"`
	}
	if code := e.post(t, "/api/templates/"+id+"/expand", map[string]any{}, &res); code != http.StatusOK {
		t.Fatalf("expand = %d", code)
	}
	if len(res.Prompts) != 1 || res.Prompts[0] != "Attendees" {
		t.Fatalf("prompts = %v", res.Prompts)
	}
	if !strings.Contains(res.Suggest, time.Now().Format("2006-01-02")) {
		t.Fatalf("suggest = %q", res.Suggest)
	}
	if !strings.Contains(res.Body, "{{prompt:Attendees}}") || !strings.Contains(res.Body, "{{title}}") {
		t.Fatalf("unanswered variables should stay as written: %q", res.Body)
	}

	var done struct {
		Body   string `json:"body"`
		Cursor int    `json:"cursor"`
	}
	req := map[string]any{
		"title":   "Kickoff",
		"folder":  "main/meetings",
		"answers": map[string]string{"Attendees": "sam and eve"},
	}
	if code := e.post(t, "/api/templates/"+id+"/expand", req, &done); code != http.StatusOK {
		t.Fatalf("expand with answers = %d", code)
	}
	if !strings.Contains(done.Body, "# Kickoff") || !strings.Contains(done.Body, "sam and eve") {
		t.Fatalf("body = %q", done.Body)
	}
	if !strings.Contains(done.Body, "in meetings.") {
		t.Fatalf("{{folder}} should be the folder inside the space: %q", done.Body)
	}
	if !strings.Contains(done.Body, time.Now().Format("2006-01-02")) {
		t.Fatalf("{{date}} missing: %q", done.Body)
	}
	if done.Cursor < 0 || !strings.HasPrefix(done.Body[done.Cursor:], "Agenda:") {
		t.Fatalf("cursor = %d body = %q", done.Cursor, done.Body)
	}
	if strings.Contains(done.Body, "{{cursor}}") {
		t.Fatalf("cursor marker left behind: %q", done.Body)
	}
}

// A note outside templates/ is not a template.
func TestTemplateExpandRejectsPlainNote(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	n, err := e.db.GetNoteByPath(context.Background(), "main/hello.md")
	if err != nil {
		t.Fatal(err)
	}
	if code := e.post(t, "/api/templates/"+n.ID+"/expand", map[string]any{}, nil); code != http.StatusBadRequest {
		t.Fatalf("plain note = %d", code)
	}
}

// A note created from an expanded body carries no template frontmatter
// and gets an id of its own, the way the daily note does.
func TestCreateFromExpandedTemplate(t *testing.T) {
	e, id := tplEnv(t, "Person.md", "---\nid: 01TEMPLATE0000000000000000\n---\n# {{title}}\n\n{{cursor}}\n")
	var res struct {
		Body string `json:"body"`
	}
	if code := e.post(t, "/api/templates/"+id+"/expand", map[string]any{}, &res); code != http.StatusOK {
		t.Fatalf("expand = %d", code)
	}
	if strings.Contains(res.Body, "01TEMPLATE0000000000000000") || strings.Contains(res.Body, "---") {
		t.Fatalf("template frontmatter leaked: %q", res.Body)
	}
	var created struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if code := e.post(t, "/api/notes", map[string]string{"path": "main/people/Kickoff.md", "content": res.Body}, &created); code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}
	raw, err := os.ReadFile(filepath.Join(e.dir, filepath.FromSlash(created.Path)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "id: "+created.ID) || strings.Contains(string(raw), "01TEMPLATE0000000000000000") {
		t.Fatalf("note on disk = %q", raw)
	}
}

// The daily note runs through the same substitution: the {{...}}
// variables fill in, the legacy {date} tokens keep working, and the
// template's frontmatter is dropped.
func TestDailyNoteTemplateVariables(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	os.MkdirAll(filepath.Join(e.dir, "main", "templates"), 0o755)
	os.WriteFile(filepath.Join(e.dir, "main", "templates", "daily.md"),
		[]byte("---\nid: 01TEMPLATE0000000000000000\n---\n# {{title}}\n\n{date} in {{folder}}\n\n- {{cursor}}\n"), 0o644)

	var res struct {
		Path string `json:"path"`
	}
	if code := e.post(t, "/api/notes/daily", map[string]string{"space": "main", "date": "2026-09-15"}, &res); code != 201 {
		t.Fatalf("daily create = %d", code)
	}
	raw, err := os.ReadFile(filepath.Join(e.dir, filepath.FromSlash(res.Path)))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{"# 2026-09-15", "2026-09-15 in journal/2026/09"} {
		if !strings.Contains(body, want) {
			t.Fatalf("daily note missing %q: %q", want, body)
		}
	}
	if !strings.HasSuffix(body, "- \n") {
		t.Fatalf("the cursor marker should be gone, the dash kept: %q", body)
	}
	if strings.Contains(body, "{{") || strings.Contains(body, "{date}") || strings.Contains(body, "01TEMPLATE0000000000000000") {
		t.Fatalf("unsubstituted template markers: %q", body)
	}
}
