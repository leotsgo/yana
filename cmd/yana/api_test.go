package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary doubles as yana: with YANA_AS_MAIN=1 it runs main, so
// the tests serve with it and the neovim plugin calls it as yana api.
func TestMain(m *testing.M) {
	if os.Getenv("YANA_AS_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	code := m.Run()
	if srvCmd != nil {
		srvCmd.Process.Kill()
		srvCmd.Wait()
		os.RemoveAll(srv.dir)
	}
	os.Exit(code)
}

const testAddr, testContentAddr = "127.0.0.1:8886", "127.0.0.1:8887"

type testServer struct {
	url     string
	dir     string
	owner   string // session file for the owner, leo
	viewer  string // session file for vic, a viewer of the space "shared"
	ownerTk string
}

var (
	srvOnce sync.Once
	srv     *testServer
	srvCmd  *exec.Cmd
	srvErr  error
)

// startServer runs one real server for the package's tests, with an
// owner, spaces home and shared, and a viewer of shared.
func startServer(t *testing.T) *testServer {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a server")
	}
	srvOnce.Do(func() { srv, srvErr = bootServer() })
	if srvErr != nil {
		t.Fatal(srvErr)
	}
	return srv
}

func bootServer() (*testServer, error) {
	dir, err := os.MkdirTemp("", "yana-cli-test")
	if err != nil {
		return nil, err
	}
	s := &testServer{url: "http://" + testAddr, dir: dir}
	srvCmd = exec.Command(os.Args[0], "serve")
	srvCmd.Env = append(os.Environ(), "YANA_AS_MAIN=1", "YANA_NOTES_ROOT="+filepath.Join(dir, "notes"),
		"YANA_LISTEN="+testAddr, "YANA_CONTENT_LISTEN="+testContentAddr, "YANA_GIT=false", "YANA_LOG_LEVEL=warn")
	log, _ := os.Create(filepath.Join(dir, "server.log"))
	srvCmd.Stdout, srvCmd.Stderr = log, log
	if err := srvCmd.Start(); err != nil {
		return nil, err
	}
	ready := false
	for i := 0; i < 100 && !ready; i++ {
		time.Sleep(100 * time.Millisecond)
		if r, err := http.Get(s.url + "/readyz"); err == nil {
			ready = r.StatusCode == 200
			r.Body.Close()
		}
	}
	if !ready {
		return nil, fmt.Errorf("the test server did not start; see %s", log.Name())
	}
	var res struct {
		Tokens tokens `json:"tokens"`
	}
	if err := call(s.url, "POST", "/api/auth/setup", "", map[string]string{"username": "leo", "password": "password-leo"}, &res); err != nil {
		return nil, err
	}
	s.ownerTk = res.Tokens.Access
	steps := []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/spaces", map[string]string{"name": "home"}},
		{"POST", "/api/spaces", map[string]string{"name": "shared"}},
		{"POST", "/api/users", map[string]string{"username": "vic", "password": "password-vic"}},
		{"PATCH", "/api/spaces/shared", map[string]any{"members": []map[string]string{{"user": "vic", "role": "viewer"}}}},
		{"POST", "/api/notes", map[string]string{"path": "shared/readme.md", "content": "Read only.\n"}},
	}
	for _, st := range steps {
		if err := call(s.url, st.method, st.path, s.ownerTk, st.body, nil); err != nil {
			return nil, fmt.Errorf("%s %s: %w", st.method, st.path, err)
		}
	}
	for _, u := range []struct{ name, pw string }{{"leo", "password-leo"}, {"vic", "password-vic"}} {
		var lr struct {
			Tokens tokens `json:"tokens"`
		}
		if err := call(s.url, "POST", "/api/auth/login", "", map[string]string{"username": u.name, "password": u.pw}, &lr); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(session{Server: s.url, Username: u.name, Access: lr.Tokens.Access, Refresh: lr.Tokens.Refresh})
		p := filepath.Join(dir, u.name+".json")
		if err := os.WriteFile(p, raw, 0o600); err != nil {
			return nil, err
		}
		if u.name == "leo" {
			s.owner = p
		} else {
			s.viewer = p
		}
	}
	return s, nil
}

// api runs one yana api verb in-process as the owner.
func api(t *testing.T, stdin string, args ...string) (map[string]any, int) {
	t.Helper()
	var out bytes.Buffer
	code := apiMain(args, strings.NewReader(stdin), &out)
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("yana api %v: not one JSON object: %q", args, out.String())
	}
	return v, code
}

func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func hasKeys(t *testing.T, what string, m map[string]any, want ...string) {
	t.Helper()
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("%s: no %q in %v", what, k, keys(m))
		}
	}
}

func TestAPIVerbs(t *testing.T) {
	s := startServer(t)
	t.Setenv("YANA_CLI_SESSION", s.owner)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	who, code := api(t, "", "whoami")
	if code != 0 || who["username"] != "leo" || who["host"] != testAddr || who["server"] != s.url {
		t.Fatalf("whoami: %d %v", code, who)
	}

	made, code := api(t, "", "create", "home/api/first")
	if code != 0 || made["path"] != "home/api/first.md" || made["kind"] != "md" || made["id"] == "" {
		t.Fatalf("create: %d %v", code, made)
	}
	id := made["id"].(string)
	if dir, code := api(t, "", "create", "home/api/sub/"); code != 0 || dir["dir"] != true || dir["path"] != "home/api/sub" {
		t.Fatalf("create folder: %d %v", code, dir)
	}

	tree, code := api(t, "", "tree")
	hasKeys(t, "tree", tree, "server", "host", "entries")
	found := false
	for _, raw := range tree["entries"].([]any) {
		e := raw.(map[string]any)
		hasKeys(t, "entry", e, "path", "name")
		if e["id"] == id {
			found = true
			hasKeys(t, "note entry", e, "kind", "title")
		}
	}
	if code != 0 || !found {
		t.Fatalf("tree: %d, note found %v", code, found)
	}

	read, code := api(t, "", "read", id)
	hasKeys(t, "read", read, "id", "path", "kind", "role", "text", "base")
	if code != 0 || read["role"] != "owner" || read["text"] != "" {
		t.Fatalf("read: %d %v", code, read)
	}

	// A save from a stale base merges with an append made since.
	body, _ := json.Marshal(map[string]any{"kind": "md", "text": "# First\n", "base": read["base"]})
	w1, code := api(t, string(body), "write", id)
	if code != 0 || w1["text"] != "# First\n" {
		t.Fatalf("write: %d %v", code, w1)
	}
	if err := call(s.url, "POST", "/api/notes/"+id+"/append", s.ownerTk, map[string]string{"content": "- from the app"}, nil); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(map[string]any{"kind": "md", "text": "# First edited\n", "base": w1["base"]})
	w2, code := api(t, string(body), "write", id)
	hasKeys(t, "write", w2, "id", "path", "text", "base")
	if code != 0 || !strings.Contains(w2["text"].(string), "# First edited") || !strings.Contains(w2["text"].(string), "- from the app") {
		t.Fatalf("merged write: %d %q", code, w2["text"])
	}

	// Search prints one hit per line.
	waitFor(t, "the search index", func() bool {
		var out bytes.Buffer
		apiMain([]string{"search", "edited"}, nil, &out)
		line, _, _ := strings.Cut(out.String(), "\n")
		var hit map[string]any
		return json.Unmarshal([]byte(line), &hit) == nil && hit["id"] == id && hit["snippet"] != nil && hit["path"] != nil
	})

	day, code := api(t, "", "daily", "2026-11-04")
	if code != 0 || !strings.Contains(day["path"].(string), "2026-11-04") || day["id"] == "" {
		t.Fatalf("daily: %d %v", code, day)
	}

	mv, code := api(t, "", "move", id, "home/api/renamed")
	if code != 0 || mv["path"] != "home/api/renamed.md" {
		t.Fatalf("move keeps the extension: %d %v", code, mv)
	}
	if res, code := api(t, "", "move", "home/api/sub/", "home/api/moved"); code != 0 || res["path"] != "home/api/moved" {
		t.Fatalf("move folder: %d %v", code, res)
	}

	waitFor(t, "the move in the tree", func() bool {
		res, code := api(t, "", "resolve", "home/api/other.md", "renamed")
		return code == 0 && res["id"] == id
	})

	rec, code := api(t, "", "recent", "--push", id)
	if ids, _ := rec["ids"].([]any); code != 0 || len(ids) == 0 || ids[0] != id {
		t.Fatalf("recent: %d %v", code, rec)
	}

	if res, code := api(t, "", "delete", id); code != 0 || len(res) != 0 {
		t.Fatalf("delete: %d %v", code, res)
	}

	// Failures: one JSON object with the error and the HTTP status.
	for _, c := range []struct {
		args   []string
		status float64
	}{
		{[]string{"read", "01AAAAAAAAAAAAAAAAAAAAAAAA"}, 404},
		{[]string{"resolve", "home/x.md", "no such note"}, 404},
		{[]string{"move", "home/", "elsewhere"}, 0},
		{[]string{"daily", "tomorrow"}, 0},
		{[]string{"frobnicate"}, 0},
		{[]string{"write", id}, 0}, // no body
	} {
		res, code := api(t, "", c.args...)
		if code != 1 || res["error"] == "" || res["status"] != c.status {
			t.Errorf("%v: %d %v", c.args, code, res)
		}
	}
	t.Setenv("YANA_CLI_SESSION", filepath.Join(t.TempDir(), "none.json"))
	if res, code := api(t, "", "tree"); code != 1 || !strings.Contains(res["error"].(string), "yana login") {
		t.Errorf("no session: %d %v", code, res)
	}
}

func TestAPIWatch(t *testing.T) {
	s := startServer(t)
	cmd := exec.Command(os.Args[0], "api", "watch", "home")
	cmd.Env = append(os.Environ(), "YANA_AS_MAIN=1", "YANA_CLI_SESSION="+s.owner)
	stdin, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(out)
	var first map[string]string
	if err := dec.Decode(&first); err != nil || first["watching"] != "home" {
		t.Fatalf("first line: %v %v", first, err)
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := call(s.url, "POST", "/api/notes", s.ownerTk, map[string]string{"path": "home/watched.md"}, &made); err != nil {
		t.Fatal(err)
	}
	call(s.url, "POST", "/api/notes/"+made.ID+"/append", s.ownerTk, map[string]string{"content": "- hello"}, nil)
	got := make(chan map[string]string)
	go func() {
		for {
			var ev map[string]string
			if dec.Decode(&ev) != nil {
				close(got)
				return
			}
			got <- ev
		}
	}()
	timeout := time.After(10 * time.Second)
	for seen := false; !seen; {
		select {
		case ev := <-got:
			seen = ev["id"] == made.ID
		case <-timeout:
			t.Fatal("no change line for the appended note")
		}
	}
	// Closing stdin ends it.
	stdin.Close()
	done := make(chan error)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("watch outlived its stdin")
	}
}

func TestCompleteFromServer(t *testing.T) {
	s := startServer(t)
	run := func(session string, words ...string) []string {
		cmd := exec.Command(os.Args[0], append([]string{"__complete"}, words...)...)
		cmd.Env = append(os.Environ(), "YANA_AS_MAIN=1", "YANA_CLI_SESSION="+session, "XDG_STATE_HOME="+t.TempDir())
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("__complete %v: %v", words, err)
		}
		return strings.Fields(string(out))
	}
	if got := run(s.owner, "open", "sha"); len(got) != 1 || got[0] != "shared/" {
		t.Errorf("open sha: %v", got)
	}
	if got := run(s.owner, "today", "--space", "h"); len(got) != 1 || got[0] != "home" {
		t.Errorf("--space h: %v", got)
	}
	if got := run(filepath.Join(t.TempDir(), "none"), "open", ""); len(got) != 0 {
		t.Errorf("no session: %v", got)
	}
}

// TestNvimPlugin runs the plugin's own tests (nvim/test/plugin_test.lua)
// in a headless neovim with no config, against the test server.
func TestNvimPlugin(t *testing.T) {
	nvim, err := exec.LookPath("nvim")
	if err != nil {
		t.Skip("nvim is not installed")
	}
	s := startServer(t)
	dir, _ := filepath.Abs("nvim")
	state := t.TempDir()
	cmd := exec.Command(nvim, "--headless", "-u", "NONE", "-i", "NONE", "--cmd", "set rtp^="+dir, "-l", filepath.Join(dir, "test", "plugin_test.lua"))
	cmd.Env = append(os.Environ(), "YANA_AS_MAIN=1", "YANA_BIN="+os.Args[0], "YANA_CLI_SESSION="+s.owner,
		"YANA_TEST_VIEWER="+s.viewer, "YANA_TEST_SERVER="+s.url, "YANA_TEST_TOKEN="+s.ownerTk,
		"XDG_STATE_HOME="+state, "XDG_CONFIG_HOME="+t.TempDir(), "XDG_DATA_HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("plugin tests failed: %v", err)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
