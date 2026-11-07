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

	"github.com/leotsgo/yana/internal/git"
	"github.com/leotsgo/yana/internal/index"
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

func bareRepo(t *testing.T) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "backup.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", bare, "init", "--bare", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("bare init: %v\n%s", err, out)
	}
	return bare
}

// fakeSSH puts an ssh on PATH that answers ls-remote with an empty ref
// list and refuses a push as a read-only deploy key.
func fakeSSH(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
*git-receive-pack*)
  echo "ERROR: The key you are authenticating with has been marked as read only." >&2
  exit 1 ;;
esac
printf 0000
`
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestGitRemotesCRUDAndPush(t *testing.T) {
	e := newGitEnv(t)
	bare := bareRepo(t)

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

	// A path remote needs no key; it is tested, saved, and pushed once.
	var created struct {
		Remote    index.GitRemote
		PushError string `json:"push_error"`
	}
	code := e.post(t, "/api/git/remotes", map[string]any{"name": "disk", "url": bare, "schedule": "hourly"}, &created)
	if code != 201 || created.PushError != "" {
		t.Fatalf("create: %d %+v", code, created)
	}
	if created.Remote.URL != bare || created.Remote.Schedule != "hourly" || !created.Remote.Enabled || created.Remote.Pushes != 1 {
		t.Fatalf("created: %+v", created.Remote)
	}
	out, err := exec.Command("git", "-C", bare, "log", "--format=%s").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "notes:") {
		t.Fatalf("remote log after create: %v %q", err, out)
	}
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "disk", "url": bare}, &errOut); code != 409 {
		t.Fatalf("duplicate name: %d", code)
	}
	if code := e.get(t, "/api/git/remotes", &list); code != 200 || len(list.Remotes) != 1 || list.Remotes[0].ID != created.Remote.ID {
		t.Fatalf("list: %d %+v", code, list)
	}

	// Edit.
	var edited index.GitRemote
	if code := e.request(t, "PUT", "/api/git/remotes/"+created.Remote.ID, map[string]any{"schedule": "nightly", "push_hour": 3}, &edited); code != 200 {
		t.Fatalf("edit: %d", code)
	}
	if edited.Schedule != "nightly" || edited.PushHour != 3 || edited.Name != "disk" {
		t.Fatalf("edited: %+v", edited)
	}
	// A new address is tested before it is saved.
	if code := e.request(t, "PUT", "/api/git/remotes/"+created.Remote.ID, map[string]any{"url": "/nonexistent/other.git"}, &errOut); code != 502 || errOut.Error == "" {
		t.Fatalf("edit to a dead url: %d %q", code, errOut.Error)
	}
	if code := e.get(t, "/api/git/remotes", &list); code != 200 || list.Remotes[0].URL != bare {
		t.Fatalf("dead url was saved: %+v", list)
	}

	// Test and push now.
	var test struct {
		OK       bool
		Branches int
	}
	if code := e.post(t, "/api/git/remotes/"+created.Remote.ID+"/test", map[string]any{}, &test); code != 200 || !test.OK {
		t.Fatalf("test: %d %+v", code, test)
	}
	var pushed struct {
		OK     bool
		Remote index.GitRemote
	}
	if code := e.post(t, "/api/git/remotes/"+created.Remote.ID+"/push", map[string]any{}, &pushed); code != 200 || pushed.Remote.Pushes != 2 || pushed.Remote.LastError != "" {
		t.Fatalf("push: %d %+v", code, pushed)
	}

	// A push to a remote that has gone away answers with the git error.
	deadBare := bareRepo(t)
	var dead struct{ Remote index.GitRemote }
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "dead", "url": deadBare}, &dead); code != 201 {
		t.Fatalf("create dead: %d", code)
	}
	if err := os.RemoveAll(deadBare); err != nil {
		t.Fatal(err)
	}
	if code := e.post(t, "/api/git/remotes/"+dead.Remote.ID+"/push", map[string]any{}, &errOut); code != 502 || errOut.Error == "" {
		t.Fatalf("dead push: %d %q", code, errOut.Error)
	}
	if code := e.get(t, "/api/git/remotes", &list); code != 200 || len(list.Remotes) != 2 {
		t.Fatalf("list: %d %d", code, len(list.Remotes))
	}
	for _, r := range list.Remotes {
		if r.ID == dead.Remote.ID && (r.LastError == "" || r.LastErrorAt.IsZero()) {
			t.Fatalf("failure not on the row: %+v", r)
		}
	}
	var status struct {
		Git struct {
			Remotes   int
			LastError string `json:"last_error"`
		}
	}
	if code := e.get(t, "/api/status", &status); code != 200 || status.Git.Remotes != 2 || status.Git.LastError == "" {
		t.Fatalf("status: %d %+v", code, status.Git)
	}

	if code := e.request(t, "DELETE", "/api/git/remotes/"+dead.Remote.ID, nil, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code := e.request(t, "DELETE", "/api/git/remotes/"+dead.Remote.ID, nil, nil); code != 404 {
		t.Fatalf("delete again: %d", code)
	}
}

func TestCreateRemoteFailsTestSavesNothing(t *testing.T) {
	e := newGitEnv(t)
	var errOut struct{ Error string }
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "gone", "url": "/nonexistent/notes.git"}, &errOut); code != 502 || errOut.Error == "" {
		t.Fatalf("unreachable path: %d %q", code, errOut.Error)
	}
	var list struct{ Remotes []index.GitRemote }
	if code := e.get(t, "/api/git/remotes", &list); code != 200 || len(list.Remotes) != 0 {
		t.Fatalf("a failed test saved a remote: %d %+v", code, list)
	}
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "gh", "url": "git@github.com:me/notes.git"}, &errOut); code != 400 || !strings.Contains(errOut.Error, "make a key first") {
		t.Fatalf("ssh without a key: %d %q", code, errOut.Error)
	}
	var key struct {
		PublicKey string `json:"public_key"`
		SealedKey string `json:"sealed_key"`
	}
	if code := e.post(t, "/api/git/keys", map[string]any{}, &key); code != 200 {
		t.Fatalf("keys: %d", code)
	}
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "p", "url": bareRepo(t), "sealed_key": key.SealedKey}, &errOut); code != 400 {
		t.Fatalf("path with a key: %d", code)
	}
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "gh", "url": "git@github.com:me/notes.git", "sealed_key": "bm90LW1pbmU="}, &errOut); code != 400 {
		t.Fatalf("foreign key: %d", code)
	}
}

func TestCreateSSHRemoteWithKey(t *testing.T) {
	fakeSSH(t)
	e := newGitEnv(t)
	var key struct {
		PublicKey string `json:"public_key"`
		SealedKey string `json:"sealed_key"`
	}
	if code := e.post(t, "/api/git/keys", map[string]any{}, &key); code != 200 || key.PublicKey == "" || key.SealedKey == "" {
		t.Fatalf("keys: %d %+v", code, key)
	}

	var raw json.RawMessage
	code := e.post(t, "/api/git/remotes", map[string]any{
		"name": "gh", "url": "git@example.invalid:me/notes.git", "sealed_key": key.SealedKey,
	}, &raw)
	if code != 201 {
		t.Fatalf("create: %d %s", code, raw)
	}
	if strings.Contains(string(raw), `"secret"`) || strings.Contains(string(raw), key.SealedKey) {
		t.Fatalf("response leaks the credential: %s", raw)
	}
	var created struct {
		Remote    index.GitRemote
		PushError string `json:"push_error"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.PushError, "Added, but the first push failed") || !strings.Contains(created.PushError, "write access") {
		t.Fatalf("push_error = %q", created.PushError)
	}
	if created.Remote.PublicKey != key.PublicKey {
		t.Fatalf("public key %q, want %q", created.Remote.PublicKey, key.PublicKey)
	}

	var rotated struct {
		PublicKey string `json:"public_key"`
	}
	if code := e.post(t, "/api/git/remotes/"+created.Remote.ID+"/key", map[string]any{}, &rotated); code != 200 || rotated.PublicKey == "" || rotated.PublicKey == key.PublicKey {
		t.Fatalf("new key: %d %+v", code, rotated)
	}
	var list struct{ Remotes []index.GitRemote }
	if code := e.get(t, "/api/git/remotes", &list); code != 200 || len(list.Remotes) != 1 || list.Remotes[0].PublicKey != rotated.PublicKey {
		t.Fatalf("list after rotating: %d %+v", code, list)
	}
	if code := e.request(t, "DELETE", "/api/git/remotes/"+created.Remote.ID+"/host-key", nil, nil); code != 200 {
		t.Fatalf("forget host key: %d", code)
	}
	// A path remote has no key to replace.
	var disk struct{ Remote index.GitRemote }
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "disk", "url": bareRepo(t)}, &disk); code != 201 {
		t.Fatalf("create disk: %d", code)
	}
	if code := e.post(t, "/api/git/remotes/"+disk.Remote.ID+"/key", map[string]any{}, nil); code != 400 {
		t.Fatalf("key for a path remote: %d", code)
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
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/git/keys"},
		{http.MethodPost, "/api/git/remotes/x/key"},
		{http.MethodDelete, "/api/git/remotes/x/host-key"},
	} {
		if code, _ := doPost(t, f.ts, c.method, c.path, w.samHdr, map[string]any{}); code != 403 {
			t.Fatalf("editor on %s %s: %d", c.method, c.path, code)
		}
	}
	if code, _ := doGet(t, f.ts, "/api/git/remotes", w.ownerHdr); code != 200 {
		t.Fatalf("owner listing remotes: %d", code)
	}
}

func TestUpdateRemoteCannotSwitchTransport(t *testing.T) {
	fakeSSH(t)
	e := newGitEnv(t)
	const want = "A remote can't switch between SSH and a folder path. Add a new remote instead."
	bare := bareRepo(t)
	var disk struct{ Remote index.GitRemote }
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "disk", "url": bare}, &disk); code != 201 {
		t.Fatalf("create disk: %d", code)
	}
	var key struct {
		SealedKey string `json:"sealed_key"`
	}
	e.post(t, "/api/git/keys", map[string]any{}, &key)
	const sshURL = "git@example.invalid:me/notes.git"
	var gh struct{ Remote index.GitRemote }
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "gh", "url": sshURL, "sealed_key": key.SealedKey}, &gh); code != 201 {
		t.Fatalf("create ssh: %d", code)
	}

	var errOut struct{ Error string }
	if code := e.request(t, "PUT", "/api/git/remotes/"+disk.Remote.ID, map[string]any{"url": sshURL}, &errOut); code != 400 || errOut.Error != want {
		t.Fatalf("path to ssh: %d %q", code, errOut.Error)
	}
	if code := e.request(t, "PUT", "/api/git/remotes/"+gh.Remote.ID, map[string]any{"url": bare}, &errOut); code != 400 || errOut.Error != want {
		t.Fatalf("ssh to path: %d %q", code, errOut.Error)
	}
	var list struct{ Remotes []index.GitRemote }
	e.get(t, "/api/git/remotes", &list)
	for _, r := range list.Remotes {
		switch r.ID {
		case disk.Remote.ID:
			if r.URL != bare || r.PublicKey != "" {
				t.Fatalf("disk row changed: %+v", r)
			}
		case gh.Remote.ID:
			if r.URL != sshURL || r.PublicKey == "" {
				t.Fatalf("ssh row changed: %+v", r)
			}
		}
	}
}
