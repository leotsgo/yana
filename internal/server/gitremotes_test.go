package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/git"
	"github.com/madeofpendletonwool/yana/internal/index"
)

func (e *gitEnv) request(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, e.ts.URL+path, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && resp.StatusCode < 500 {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

func TestGitRemotesCRUDAndPush(t *testing.T) {
	e := newGitEnv(t)
	bare := filepath.Join(t.TempDir(), "backup.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", bare, "init", "--bare", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("bare init: %v\n%s", err, out)
	}

	var list struct{ Remotes []index.GitRemote }
	if code := e.get(t, "/api/git/remotes", &list); code != 200 || len(list.Remotes) != 0 {
		t.Fatalf("empty list: %d %+v", code, list)
	}

	// Validation.
	var errOut struct{ Error string }
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "x", "url": "--evil"}, &errOut); code != 400 {
		t.Fatalf("bad url accepted: %d %s", code, errOut.Error)
	}
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "x", "url": bare, "schedule": "sometimes"}, &errOut); code != 400 {
		t.Fatalf("bad schedule accepted: %d", code)
	}

	// Create; a pasted credential moves out of the URL and is never echoed.
	var created index.GitRemote
	code := e.post(t, "/api/git/remotes", map[string]any{
		"name": "github", "url": "https://me:ghp_secret@github.com/me/notes.git", "schedule": "hourly",
	}, &created)
	if code != 201 {
		t.Fatalf("create: %d", code)
	}
	if created.URL != "https://github.com/me/notes.git" || created.Username != "me" || !created.HasSecret || created.Schedule != "hourly" || !created.Enabled {
		t.Fatalf("created: %+v", created)
	}
	raw, _ := json.Marshal(created)
	if strings.Contains(string(raw), "ghp_secret") {
		t.Fatal("response leaks the token")
	}
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "github", "url": bare}, &errOut); code != 409 {
		t.Fatalf("duplicate name: %d", code)
	}

	// Edit: an empty token keeps the stored one; clear_token drops it.
	var edited index.GitRemote
	if code := e.request(t, "PUT", "/api/git/remotes/"+created.ID, map[string]any{"schedule": "nightly", "push_hour": 3}, &edited); code != 200 {
		t.Fatalf("edit: %d", code)
	}
	if !edited.HasSecret || edited.Schedule != "nightly" || edited.PushHour != 3 || edited.Name != "github" {
		t.Fatalf("edited: %+v", edited)
	}
	if code := e.request(t, "PUT", "/api/git/remotes/"+created.ID, map[string]any{"clear_token": true}, &edited); code != 200 || edited.HasSecret {
		t.Fatalf("clear token: %d %+v", code, edited)
	}

	// A local remote pushes for real, after committing what is pending.
	var local index.GitRemote
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "disk", "url": bare, "schedule": "commit"}, &local); code != 201 {
		t.Fatalf("create local: %d", code)
	}
	var test struct {
		OK       bool
		Branches int
	}
	if code := e.post(t, "/api/git/remotes/"+local.ID+"/test", map[string]any{}, &test); code != 200 || !test.OK {
		t.Fatalf("test: %d %+v", code, test)
	}
	var pushed struct {
		OK     bool
		Remote index.GitRemote
	}
	if code := e.post(t, "/api/git/remotes/"+local.ID+"/push", map[string]any{}, &pushed); code != 200 || pushed.Remote.Pushes != 1 || pushed.Remote.LastError != "" {
		t.Fatalf("push: %d %+v", code, pushed)
	}
	out, err := exec.Command("git", "-C", bare, "log", "--format=%s").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "notes:") {
		t.Fatalf("remote log: %v %q", err, out)
	}

	// A push to an unreachable remote answers with the git error.
	var dead index.GitRemote
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "dead", "url": filepath.Join(t.TempDir(), "nope.git")}, &dead); code != 201 {
		t.Fatalf("create dead: %d", code)
	}
	if code := e.post(t, "/api/git/remotes/"+dead.ID+"/push", map[string]any{}, &errOut); code != 502 || errOut.Error == "" {
		t.Fatalf("dead push: %d %q", code, errOut.Error)
	}
	if code := e.get(t, "/api/git/remotes", &list); code != 200 || len(list.Remotes) != 3 {
		t.Fatalf("list: %d %d", code, len(list.Remotes))
	}
	for _, r := range list.Remotes {
		if r.ID == dead.ID && (r.LastError == "" || r.LastErrorAt.IsZero()) {
			t.Fatalf("failure not on the row: %+v", r)
		}
	}
	var status struct {
		Git struct {
			Remotes   int
			LastError string `json:"last_error"`
		}
	}
	if code := e.get(t, "/api/status", &status); code != 200 || status.Git.Remotes != 3 || status.Git.LastError == "" {
		t.Fatalf("status: %d %+v", code, status.Git)
	}

	if code := e.request(t, "DELETE", "/api/git/remotes/"+dead.ID, nil, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code := e.request(t, "DELETE", "/api/git/remotes/"+dead.ID, nil, nil); code != 404 {
		t.Fatalf("delete again: %d", code)
	}
}

func TestGitRemotesNeedOwner(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)
	gl := git.New(f.dir, git.Options{Quiet: time.Hour, Interval: time.Hour, DB: f.db}, nil)
	if err := gl.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.srv.Git = gl
	if code, _ := doGet(t, f.ts, "/api/git/remotes", w.samHdr); code != 403 {
		t.Fatalf("editor listing remotes: %d", code)
	}
	if code, _ := doPost(t, f.ts, http.MethodPost, "/api/git/remotes", w.samHdr, map[string]any{"name": "x", "url": "/tmp/x.git"}); code != 403 {
		t.Fatalf("editor creating a remote: %d", code)
	}
	if code, _ := doGet(t, f.ts, "/api/git/remotes", w.ownerHdr); code != 200 {
		t.Fatalf("owner listing remotes: %d", code)
	}
}
