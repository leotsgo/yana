package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/auth"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// fixture is a full stack: root, index, scanner, reconciliation loop
// with fast timings, auth, and the MCP handler behind a test server.
type fixture struct {
	dir   string
	root  *pathsafe.Root
	db    *index.DB
	sc    *scanner.Scanner
	rec   *reconcile.Reconciler
	as    *auth.Service
	h     *Handler
	ts    *httptest.Server
	token string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	root, err := pathsafe.NewRoot(dir, pathsafe.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(filepath.Join(dir, ".sync", "index.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sc := scanner.New(root, db, scanner.Options{SettleTime: 50 * time.Millisecond}, log)
	rec := reconcile.New(root, db, sc, reconcile.Options{
		IdleTime: 100 * time.Millisecond, Debounce: 30 * time.Millisecond,
		SettleTime: 300 * time.Millisecond, UnloadAfter: -1,
	}, log)
	if err := rec.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := rec.Close(); err != nil {
			t.Error(err)
		}
	})
	as, err := auth.Open(db, filepath.Join(dir, ".sync", "auth_secret"), auth.Options{}, log)
	if err != nil {
		t.Fatal(err)
	}
	h := New(Deps{
		DB: db, Root: root, Sync: rec, Scanner: sc, Auth: as,
		Limit:   pathsafe.NewRateLimiter(pathsafe.Rate{N: 1000, Window: time.Minute}, pathsafe.Rate{N: 1000, Window: time.Minute}),
		Log:     log,
		Version: "test",
	})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	f := &fixture{dir: dir, root: root, db: db, sc: sc, rec: rec, as: as, h: h, ts: ts}
	owner := auth.Identity{UserID: "u1", Username: "owner", Owner: true}
	tok, err := as.CreateAgentToken(context.Background(), owner, "docs", []string{"homelab"}, true)
	if err != nil {
		t.Fatal(err)
	}
	f.token = tok.Secret
	return f
}

// dropNote writes a note file and indexes it, the way a prior phase
// would have.
func (f *fixture) dropNote(t *testing.T, rel, body string) string {
	t.Helper()
	id := scanner.NewID(time.Now())
	abs := filepath.Join(f.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	note := fmt.Sprintf("---\nid: %s\ncreated: 2026-09-15T00:00:00Z\n---\n%s", id, body)
	if err := os.WriteFile(abs, []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.sc.ScanOne(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	return id
}

// call posts one tools/call request and returns the result text and
// isError flag.
func (f *fixture) call(t *testing.T, tool string, args map[string]any) (string, bool) {
	t.Helper()
	var raw []byte
	if args == nil {
		raw = []byte(`{}`)
	} else {
		raw, _ = json.Marshal(args)
	}
	body, isErr := f.rpc(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": json.RawMessage(raw)},
	})
	if isErr {
		return string(body), true
	}
	var res toolResult
	if err := json.Unmarshal(body, &res); err != nil || len(res.Content) == 0 {
		return string(body), false
	}
	return res.Content[0]["text"], res.IsError
}

// rpc posts one JSON-RPC request and returns the raw result JSON. The
// second return is true when the call itself failed (a JSON-RPC error
// or an HTTP error), in which case the first return carries the reason.
func (f *fixture) rpc(t *testing.T, msg map[string]any) (json.RawMessage, bool) {
	t.Helper()
	body, _ := json.Marshal(msg)
	req, _ := http.NewRequest("POST", f.ts.URL, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return raw, true
	}
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("rpc response is not JSON-RPC: %s", raw)
	}
	if out.Error != nil {
		t.Fatalf("rpc error %d: %s", out.Error.Code, out.Error.Message)
	}
	return out.Result, false
}

func TestInitializeAndToolList(t *testing.T) {
	f := newFixture(t)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-06-18"},
	})
	req, _ := http.NewRequest("POST", f.ts.URL, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status %d", resp.StatusCode)
	}
	var init struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&init); err != nil {
		t.Fatal(err)
	}
	if init.Result.ProtocolVersion != "2025-06-18" || init.Result.ServerInfo.Name != "yana" {
		t.Fatalf("initialize answered wrong: %+v", init.Result)
	}

	// tools/list via the raw rpc helper.
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	raw, isErr := f.rpc(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	if isErr {
		t.Fatal("tools/list errored")
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("tools/list result is not the expected shape: %v (%s)", err, raw)
	}
	want := []string{"list_spaces", "list_tree", "read_note", "write_note", "append_note", "search_notes", "move_note"}
	if len(list.Tools) != len(want) {
		t.Fatalf("expected %d tools, got %d", len(want), len(list.Tools))
	}
	for i, w := range want {
		if list.Tools[i].Name != w {
			t.Fatalf("tool %d is %s, want %s", i, list.Tools[i].Name, w)
		}
	}

	// A notification answers 202 with no body.
	note, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	req2, _ := http.NewRequest("POST", f.ts.URL, bytes.NewReader(note))
	req2.Header.Set("Authorization", "Bearer "+f.token)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("notification status %d", resp2.StatusCode)
	}

	// Unknown method answers a JSON-RPC error, not a silent 200.
	_, _ = f.rpcErr(t, "no/such/method")
}

func (f *fixture) rpcErr(t *testing.T, method string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 9, "method": method})
	req, _ := http.NewRequest("POST", f.ts.URL, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Error *rpcError `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error == nil {
		t.Fatal("expected a JSON-RPC error")
	}
	return out.Error.Code, out.Error.Message
}

func TestUnauthorizedAndMethod(t *testing.T) {
	f := newFixture(t)
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"})
	req, _ := http.NewRequest("POST", f.ts.URL, bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token status %d", resp.StatusCode)
	}
	if code, _ := f.rpcErr(t, "bogus"); code != codeMethod {
		t.Fatalf("unknown method code %d", code)
	}
	// GET is not the transport.
	resp2, err := http.Get(f.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status %d", resp2.StatusCode)
	}
}

func TestWriteNoteCreatesWithAgentAuthor(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	txt, isErr := f.call(t, "write_note", map[string]any{
		"space": "homelab", "path": "servers/pve.md", "content": "# Proxmox\n\nHosts the VMs. See [[network]].",
	})
	if isErr {
		t.Fatalf("write_note failed: %s", txt)
	}
	var res struct {
		ID      string `json:"id"`
		Path    string `json:"path"`
		Created bool   `json:"created"`
	}
	if err := json.Unmarshal([]byte(txt), &res); err != nil {
		t.Fatalf("bad result: %s", txt)
	}
	if !res.Created || res.Path != "homelab/servers/pve.md" || len(res.ID) != 26 {
		t.Fatalf("bad create result: %+v", res)
	}

	// The write is attributed to the agent in the update log.
	ups, err := f.db.Updates(ctx, res.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	authors := map[string]bool{}
	for _, u := range ups {
		authors[u.Author] = true
	}
	if !authors["agent:docs"] {
		t.Fatalf("no agent:docs author in the log: %v", authors)
	}
	for a := range authors {
		if a != "agent:docs" {
			t.Fatalf("unexpected author %q in a fresh note's log", a)
		}
	}

	// The document holds the content.
	doc, err := f.rec.Text(ctx, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, "# Proxmox") || !strings.Contains(doc, "[[network]]") {
		t.Fatalf("document content wrong: %q", doc)
	}

	// The file on disk carries frontmatter once the loop settles.
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, err := os.ReadFile(filepath.Join(f.dir, "homelab", "servers", "pve.md"))
		if err == nil && strings.Contains(string(raw), "id: ") && strings.Contains(string(raw), "# Proxmox") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("file never settled: %v %q", err, raw)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A second write replaces through the document, keeping the id.
	txt, isErr = f.call(t, "write_note", map[string]any{
		"space": "homelab", "path": "servers/pve.md", "content": "# Proxmox VE\n\nRewritten.",
	})
	if isErr {
		t.Fatalf("rewrite failed: %s", txt)
	}
	var res2 struct {
		ID      string `json:"id"`
		Created bool   `json:"created"`
	}
	if err := json.Unmarshal([]byte(txt), &res2); err != nil {
		t.Fatal(err)
	}
	if res2.Created || res2.ID != res.ID {
		t.Fatalf("replace changed the note: %+v", res2)
	}
}

func TestWriteNoteScopeAndSafety(t *testing.T) {
	f := newFixture(t)
	// Out of scope.
	if txt, isErr := f.call(t, "write_note", map[string]any{"space": "secret", "path": "a.md", "content": "x"}); !isErr {
		t.Fatalf("out-of-scope write accepted: %s", txt)
	}
	// Read-only token.
	owner := auth.Identity{UserID: "u1", Owner: true}
	ro, err := f.as.CreateAgentToken(context.Background(), owner, "reader", []string{"homelab"}, false)
	if err != nil {
		t.Fatal(err)
	}
	saved := f.token
	f.token = ro.Secret
	defer func() { f.token = saved }()
	if txt, isErr := f.call(t, "write_note", map[string]any{"space": "homelab", "path": "a.md", "content": "x"}); !isErr {
		t.Fatalf("read-only write accepted: %s", txt)
	}
	if txt, isErr := f.call(t, "list_spaces", nil); isErr {
		t.Fatalf("read-only list failed: %s", txt)
	} else if !strings.Contains(txt, "homelab") {
		t.Fatalf("spaces missing scope: %s", txt)
	}
	f.token = saved

	// Traversal and asset paths are refused.
	for _, p := range []string{"../escape.md", "sub/_assets/x.md", "no-extension"} {
		if txt, isErr := f.call(t, "write_note", map[string]any{"space": "homelab", "path": p, "content": "x"}); !isErr {
			t.Fatalf("path %q accepted: %s", p, txt)
		}
	}
	// A path that cleans into another space is a rejection, not a
	// redirect past the scope.
	for _, p := range []string{"../secret/x.md", "homelab/../secret/x.md"} {
		if txt, isErr := f.call(t, "write_note", map[string]any{"space": "homelab", "path": p, "content": "x"}); !isErr {
			t.Fatalf("escaping path %q accepted: %s", p, txt)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dir, "secret")); err == nil {
		t.Fatal("the escape created a directory outside the scope")
	}
	if txt, isErr := f.call(t, "list_tree", map[string]any{"space": "homelab", "path": "../secret"}); !isErr {
		t.Fatalf("escaping tree prefix accepted: %s", txt)
	}
	// A bad agent_label is refused without touching anything.
	if txt, isErr := f.call(t, "write_note", map[string]any{"space": "homelab", "path": "ok.md", "content": "x", "agent_label": "we:ird"}); !isErr {
		t.Fatalf("bad label accepted: %s", txt)
	}
}

func TestAppendReadSearchTree(t *testing.T) {
	f := newFixture(t)
	id := f.dropNote(t, "homelab/network.md", "# Network\n\nVLANs and switch config.")

	if txt, isErr := f.call(t, "read_note", map[string]any{"note": id}); isErr {
		t.Fatalf("read by id failed: %s", txt)
	} else if !strings.Contains(txt, "VLANs") || !strings.Contains(txt, `"path": "homelab/network.md"`) {
		t.Fatalf("read by id wrong: %s", txt)
	}
	if txt, isErr := f.call(t, "read_note", map[string]any{"note": "homelab/network.md"}); isErr {
		t.Fatalf("read by path failed: %s", txt)
	} else if !strings.Contains(txt, id) {
		t.Fatalf("read by path wrong id: %s", txt)
	}
	if txt, isErr := f.call(t, "read_note", map[string]any{"note": "01AAAAAAAAAAAAAAAAAAAAAAAA"}); !isErr {
		t.Fatalf("missing note read: %s", txt)
	}

	if txt, isErr := f.call(t, "append_note", map[string]any{"note": "homelab/network.md", "content": "\nAdded later."}); isErr {
		t.Fatalf("append failed: %s", txt)
	}
	doc, err := f.rec.Text(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(doc, "Added later.") {
		t.Fatalf("append did not land: %q", doc)
	}
	// The append is agent-attributed.
	ups, _ := f.db.Updates(context.Background(), id, 0)
	last := ups[len(ups)-1]
	if last.Author != "agent:docs" {
		t.Fatalf("append author %q", last.Author)
	}

	if txt, isErr := f.call(t, "search_notes", map[string]any{"query": "switch config"}); isErr {
		t.Fatalf("search failed: %s", txt)
	} else if !strings.Contains(txt, "homelab/network.md") {
		t.Fatalf("search missed the note: %s", txt)
	}
	// The appended text is searchable once written back and reindexed.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if txt, _ := f.call(t, "search_notes", map[string]any{"query": "Added later"}); strings.Contains(txt, "network.md") {
			break
		} else if time.Now().After(deadline) {
			t.Fatal("append never became searchable")
		}
		time.Sleep(50 * time.Millisecond)
	}

	if txt, isErr := f.call(t, "list_tree", map[string]any{"space": "homelab"}); isErr {
		t.Fatalf("list_tree failed: %s", txt)
	} else if !strings.Contains(txt, "network.md") || !strings.Contains(txt, id) {
		t.Fatalf("tree wrong: %s", txt)
	}
	if txt, isErr := f.call(t, "list_tree", map[string]any{"space": "secret"}); !isErr {
		t.Fatalf("out-of-scope tree accepted: %s", txt)
	}
	if txt, isErr := f.call(t, "list_spaces", nil); isErr {
		t.Fatalf("list_spaces failed: %s", txt)
	}
}

func TestMoveNoteRewritesLinks(t *testing.T) {
	f := newFixture(t)
	id := f.dropNote(t, "homelab/network.md", "# Network\n\nConfig.")
	f.dropNote(t, "homelab/index.md", "# Index\n\nStart at [[network]].")

	txt, isErr := f.call(t, "move_note", map[string]any{"note": id, "new_path": "homelab/net/setup.md"})
	if isErr {
		t.Fatalf("move failed: %s", txt)
	}
	var res struct {
		Path      string `json:"path"`
		Rewritten int    `json:"rewritten"`
	}
	if err := json.Unmarshal([]byte(txt), &res); err != nil {
		t.Fatal(err)
	}
	if res.Path != "homelab/net/setup.md" {
		t.Fatalf("move path wrong: %s", txt)
	}
	// The inbound link was rewritten.
	deadline := time.Now().Add(5 * time.Second)
	for {
		doc, err := f.rec.Text(context.Background(), f.noteIDByPath(t, "homelab/index.md"))
		if err == nil && strings.Contains(doc, "[[net/setup") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("link rewrite never landed")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// A move into an out-of-scope space is refused.
	if txt, isErr := f.call(t, "move_note", map[string]any{"note": id, "new_path": "secret/x.md"}); !isErr {
		t.Fatalf("cross-space move accepted: %s", txt)
	}
}

func (f *fixture) noteIDByPath(t *testing.T, rel string) string {
	t.Helper()
	n, err := f.db.GetNoteByPath(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	return n.ID
}

func TestAgentWriteReachesSubscribers(t *testing.T) {
	f := newFixture(t)
	id := f.dropNote(t, "homelab/network.md", "# Network\n\nOld text.")

	// A subscriber sees the agent's rewrite as an update authored by
	// the agent — the live path a phone client takes.
	events := make(chan reconcile.Event, 8)
	unsub := f.rec.Subscribe(func(ev reconcile.Event) {
		if ev.Kind == reconcile.EventUpdate {
			select {
			case events <- ev:
			default:
			}
		}
	})
	defer unsub()

	if txt, isErr := f.call(t, "write_note", map[string]any{
		"space": "homelab", "path": "network.md", "content": "# Network\n\nNew text from the agent.",
	}); isErr {
		t.Fatalf("write failed: %s", txt)
	}
	select {
	case ev := <-events:
		if ev.Author != "agent:docs" || ev.NoteID != id {
			t.Fatalf("event wrong: %+v", ev)
		}
		if !strings.Contains(f.docText(t, id), "New text from the agent.") {
			t.Fatal("document not updated")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no update event reached subscribers")
	}
}

func (f *fixture) docText(t *testing.T, id string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	txt, err := f.rec.Text(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return txt
}

func TestRateLimit(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	root, err := pathsafe.NewRoot(dir, pathsafe.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(filepath.Join(dir, ".sync", "index.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sc := scanner.New(root, db, scanner.Options{SettleTime: 50 * time.Millisecond}, log)
	rec := reconcile.New(root, db, sc, reconcile.Options{UnloadAfter: -1}, log)
	if err := rec.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rec.Close() })
	as, err := auth.Open(db, filepath.Join(dir, ".sync", "auth_secret"), auth.Options{}, log)
	if err != nil {
		t.Fatal(err)
	}
	owner := auth.Identity{UserID: "u1", Owner: true}
	tok, err := as.CreateAgentToken(context.Background(), owner, "burst", []string{"s"}, true)
	if err != nil {
		t.Fatal(err)
	}
	h := New(Deps{
		DB: db, Root: root, Sync: rec, Scanner: sc, Auth: as,
		Limit: pathsafe.NewRateLimiter(pathsafe.Rate{N: 1000, Window: time.Minute}, pathsafe.Rate{N: 2, Window: time.Minute}),
		Log:   log,
	})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	call := func(i int) bool {
		body, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": i, "method": "tools/call",
			"params": map[string]any{"name": "write_note", "arguments": map[string]any{
				"space": "s", "path": "n.md", "content": "x",
			}},
		})
		req, _ := http.NewRequest("POST", ts.URL, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok.Secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			Result *toolResult `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.Result != nil && out.Result.IsError
	}
	if call(1) {
		t.Fatal("first write refused")
	}
	if call(2) {
		t.Fatal("second write refused")
	}
	if !call(3) {
		t.Fatal("third write was not rate limited")
	}
}
