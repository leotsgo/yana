package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leotsgo/yana/internal/auth"
	"github.com/leotsgo/yana/internal/drawing"
	"github.com/leotsgo/yana/internal/drawing/flowchart"
	"github.com/leotsgo/yana/internal/frontmatter"
	"github.com/leotsgo/yana/internal/index"
	"github.com/leotsgo/yana/internal/pathsafe"
	"github.com/leotsgo/yana/internal/reconcile"
	"github.com/leotsgo/yana/internal/scanner"
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
	want := []string{"list_spaces", "list_tree", "read_note", "write_note", "append_note", "search_notes", "move_note", "upload_asset", "draw_flowchart", "add_drawing_image"}
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

// TestToolSchemasAreValid checks the JSON a client actually receives, not just
// the tool names. A nil variadic in toolList's req() helper marshalled as
// "required": null, which is invalid JSON Schema; strict clients (Claude Code)
// rejected the whole tools/list response and were left with no usable tools.
func TestToolSchemasAreValid(t *testing.T) {
	f := newFixture(t)
	raw, isErr := f.rpc(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	if isErr {
		t.Fatal("tools/list errored")
	}
	if bytes.Contains(raw, []byte(`"required":null`)) {
		t.Fatalf("a tool schema serialised a null required: %s", raw)
	}

	var list struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Type       string                     `json:"type"`
				Properties map[string]json.RawMessage `json:"properties"`
				Required   *[]string                  `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("tools/list is not the expected shape: %v (%s)", err, raw)
	}
	if len(list.Tools) == 0 {
		t.Fatal("tools/list returned no tools")
	}

	for _, tl := range list.Tools {
		if tl.Name == "" || tl.Description == "" {
			t.Errorf("tool %q: name and description are both required", tl.Name)
		}
		if tl.InputSchema.Type != "object" {
			t.Errorf("%s: inputSchema type is %q, want \"object\"", tl.Name, tl.InputSchema.Type)
		}
		if tl.InputSchema.Required == nil {
			continue // absent is valid, and means everything is optional
		}
		req := *tl.InputSchema.Required
		if len(req) == 0 {
			t.Errorf("%s: required is present but empty — omit it instead", tl.Name)
		}
		for _, name := range req {
			if _, declared := tl.InputSchema.Properties[name]; !declared {
				t.Errorf("%s: required names %q, which is not a declared property", tl.Name, name)
			}
		}
	}
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

func TestUploadAsset(t *testing.T) {
	f := newFixture(t)
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG fake"))
	up := func(args map[string]any) (map[string]any, string, bool) {
		txt, isErr := f.call(t, "upload_asset", args)
		var res map[string]any
		_ = json.Unmarshal([]byte(txt), &res)
		return res, txt, isErr
	}
	args := map[string]any{"space": "homelab", "note": "servers/pve.md", "filename": "rack diagram.png", "data": png}
	res, txt, isErr := up(args)
	if isErr {
		t.Fatalf("upload failed: %s", txt)
	}
	if res["path"] != "homelab/servers/_assets/rack-diagram.png" || res["markdown"] != "![rack diagram](_assets/rack-diagram.png)" {
		t.Fatalf("bad result: %s", txt)
	}
	got, err := os.ReadFile(filepath.Join(f.dir, "homelab/servers/_assets/rack-diagram.png"))
	if err != nil || string(got) != "\x89PNG fake" {
		t.Fatalf("file not written: %v %q", err, got)
	}

	// Same name again gets a suffix; a data: URL decodes too.
	args["data"] = "data:image/png;base64," + png
	if res, txt, _ = up(args); res["path"] != "homelab/servers/_assets/rack-diagram-2.png" {
		t.Fatalf("expected suffixed name: %s", txt)
	}

	// Non-images link without the bang.
	args["filename"] = "manual.pdf"
	if res, txt, _ = up(args); res["markdown"] != "[manual.pdf](_assets/manual.pdf)" {
		t.Fatalf("bad pdf link: %s", txt)
	}

	for _, bad := range []map[string]any{
		{"space": "secret", "note": "a.md", "filename": "x.png", "data": png},
		{"space": "homelab", "note": "a.md", "filename": "../x.png", "data": png},
		{"space": "homelab", "note": "a.md", "filename": "x.md", "data": png},
		{"space": "homelab", "note": "a.md", "filename": "x.png", "data": "not base64!"},
		{"space": "homelab", "note": "../secret/a.md", "filename": "x.png", "data": png},
	} {
		if _, txt, isErr := up(bad); !isErr {
			t.Fatalf("accepted %v: %s", bad, txt)
		}
	}
}

const drawingRefused = "drawings are changed with draw_flowchart and add_drawing_image, not this tool"

const testScene = `{"type":"excalidraw","version":2,"source":"yana","elements":[` +
	`{"type":"text","text":"Router","originalText":"Router","link":"[[Target]]"},` +
	`{"type":"rectangle","link":null}],"appState":{},"files":{}}`

func TestMCPReadDrawing(t *testing.T) {
	f := newFixture(t)
	f.dropNote(t, "homelab/Map.excalidraw.md", string(drawing.Encode([]byte(testScene))))
	f.dropNote(t, "homelab/Bad.excalidraw.md", "not a scene\n")

	txt, isErr := f.call(t, "read_note", map[string]any{"note": "homelab/Map.excalidraw.md"})
	if isErr {
		t.Fatalf("read failed: %s", txt)
	}
	raw, _ := os.ReadFile(filepath.Join(f.dir, "homelab", "Map.excalidraw.md"))
	var res struct{ Base, Content string }
	if err := json.Unmarshal([]byte(txt), &res); err != nil {
		t.Fatal(err)
	}
	if res.Base != drawing.Hash(raw) || !strings.Contains(res.Content, `text="Router"`) ||
		!strings.Contains(res.Content, "link=[[Target]]") ||
		!strings.Contains(res.Content, "layout:") || strings.Contains(txt, `"elements"`) {
		t.Fatalf("drawing read wrong: %s", txt)
	}
	txt, isErr = f.call(t, "read_note", map[string]any{"note": "homelab/Bad.excalidraw.md"})
	if isErr || !strings.Contains(txt, "Excalidraw drawing (could not be read).") || strings.Contains(txt, `"base"`) {
		t.Fatalf("unreadable drawing: %v %s", isErr, txt)
	}
	// A drawing is read from its file: no CRDT document is made for it.
	if n := f.rec.Stats().Loaded; n != 0 {
		t.Fatalf("reading drawings loaded %d documents", n)
	}
}

func TestMCPWriteDrawingRefused(t *testing.T) {
	f := newFixture(t)
	txt, isErr := f.call(t, "write_note", map[string]any{"space": "homelab", "path": "New.excalidraw.md", "content": "x"})
	if !isErr || !strings.Contains(txt, drawingRefused) {
		t.Fatalf("write accepted: %v %s", isErr, txt)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "homelab", "New.excalidraw.md")); err == nil {
		t.Fatal("file was created")
	}
}

func TestMCPAppendDrawingRefused(t *testing.T) {
	f := newFixture(t)
	f.dropNote(t, "homelab/Map.excalidraw.md", string(drawing.Encode([]byte(testScene))))
	abs := filepath.Join(f.dir, "homelab", "Map.excalidraw.md")
	before, _ := os.ReadFile(abs)
	txt, isErr := f.call(t, "append_note", map[string]any{"note": "homelab/Map.excalidraw.md", "content": "more"})
	if !isErr || !strings.Contains(txt, drawingRefused) {
		t.Fatalf("append accepted: %v %s", isErr, txt)
	}
	if after, _ := os.ReadFile(abs); !bytes.Equal(before, after) {
		t.Fatal("drawing file changed")
	}
}

// callAll is call returning every content block.
func (f *fixture) callAll(t *testing.T, tool string, args map[string]any) ([]map[string]string, bool) {
	t.Helper()
	raw, _ := json.Marshal(args)
	body, isErr := f.rpc(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": json.RawMessage(raw)},
	})
	if isErr {
		t.Fatalf("%s: %s", tool, body)
	}
	var res toolResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	return res.Content, res.IsError
}

type drawResult struct {
	ID, Path, Embed, Base string
	Created               bool
	Stats                 map[string]int
}

// draw calls draw_flowchart and fails the test on an error.
func (f *fixture) draw(t *testing.T, args map[string]any) drawResult {
	t.Helper()
	if args["space"] == nil {
		args["space"] = "homelab"
	}
	txt, isErr := f.call(t, "draw_flowchart", args)
	if isErr {
		t.Fatalf("draw_flowchart failed: %s", txt)
	}
	var r drawResult
	if err := json.Unmarshal([]byte(txt), &r); err != nil {
		t.Fatalf("bad result: %s", txt)
	}
	return r
}

func (f *fixture) readFile(t *testing.T, rel string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// nodeAt reads a drawing file and returns the layout item for id.
func nodeAt(t *testing.T, file []byte, id string) flowchart.Item {
	t.Helper()
	_, scene, err := drawing.Parse(frontmatter.Parse(file).Body)
	if err != nil {
		t.Fatal(err)
	}
	v, err := flowchart.Read(scene)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range v.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %s", id)
	return flowchart.Item{}
}

const archMermaid = "flowchart LR\n  a[Router] --> b[Switch]\n"

func TestDrawFlowchartCreates(t *testing.T) {
	f := newFixture(t)
	r := f.draw(t, map[string]any{"path": "arch.excalidraw.md", "mermaid": archMermaid})
	if !r.Created || r.Path != "homelab/arch.excalidraw.md" || len(r.ID) != 26 || r.Embed != "![[arch]]" {
		t.Fatalf("bad result: %+v", r)
	}
	if r.Stats["nodes_added"] != 2 || r.Stats["edges_added"] != 1 {
		t.Fatalf("bad stats: %+v", r.Stats)
	}
	raw := f.readFile(t, r.Path)
	fm := frontmatter.Parse(raw)
	if fm.Meta.ID != r.ID {
		t.Fatalf("frontmatter id %q", fm.Meta.ID)
	}
	if _, _, err := drawing.Parse(fm.Body); err != nil {
		t.Fatalf("file does not parse: %v", err)
	}
	if r.Base != drawing.Hash(raw) {
		t.Fatal("base is not the file's hash")
	}
	txt, isErr := f.call(t, "read_note", map[string]any{"note": r.Path})
	var read struct{ Base, Content string }
	if isErr || json.Unmarshal([]byte(txt), &read) != nil {
		t.Fatalf("read failed: %s", txt)
	}
	if read.Base != r.Base || !strings.Contains(read.Content, "flowchart") || !strings.Contains(read.Content, "layout:") {
		t.Fatalf("read wrong: %s", txt)
	}
	// A drawing in a folder embeds by name too.
	deep := f.draw(t, map[string]any{"path": "sub/deep.excalidraw.md", "mermaid": archMermaid})
	if deep.Embed != "![[deep]]" {
		t.Fatalf("embed %q", deep.Embed)
	}
	// Two drawings with one name: the name is ambiguous, so the path is used.
	other := f.draw(t, map[string]any{"path": "other/deep.excalidraw.md", "mermaid": archMermaid})
	if other.Embed != "![[other/deep.excalidraw]]" {
		t.Fatalf("embed %q", other.Embed)
	}
	// Every embed resolves to its drawing through the resolver the
	// server's embed inlining uses, from notes anywhere in the space.
	res, err := f.db.SpaceResolver(context.Background(), "homelab")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []drawResult{r, other} {
		target := strings.TrimSuffix(strings.TrimPrefix(d.Embed, "![["), "]]")
		for _, from := range []string{"homelab/n.md", "homelab/other/n.md", "homelab/x/y/n.md"} {
			if got := res.Resolve(target, from); !got.OK || got.ToID != d.ID {
				t.Errorf("%s from %s resolves to %+v, want %s", d.Embed, from, got, d.ID)
			}
		}
	}
}

func TestDrawFlowchartUpdatesWithBase(t *testing.T) {
	f := newFixture(t)
	r := f.draw(t, map[string]any{"path": "arch.excalidraw.md", "mermaid": archMermaid})
	before := f.readFile(t, r.Path)
	a := nodeAt(t, before, "a")

	r2 := f.draw(t, map[string]any{"path": "arch.excalidraw.md", "mermaid": "flowchart LR\n  a --> c[Server]\n", "base": r.Base})
	if r2.Created || r2.ID != r.ID || r2.Stats["nodes_added"] != 1 || r2.Base == r.Base {
		t.Fatalf("bad update: %+v", r2)
	}
	after := f.readFile(t, r.Path)
	if r2.Base != drawing.Hash(after) {
		t.Fatal("base is not the new file's hash")
	}
	if a2 := nodeAt(t, after, "a"); a2.X != a.X || a2.Y != a.Y {
		t.Fatalf("node a moved: %+v -> %+v", a, a2)
	}
	nodeAt(t, after, "c")
	nodeAt(t, after, "b")

	// A stale base writes nothing.
	txt, isErr := f.call(t, "draw_flowchart", map[string]any{"space": "homelab", "path": "arch.excalidraw.md", "mermaid": "flowchart LR\n  d[New]\n", "base": r.Base})
	if !isErr || txt != "the drawing changed since you read it; read it again" {
		t.Fatalf("stale base accepted: %v %s", isErr, txt)
	}
	// No base on an existing drawing is an error too.
	if txt, isErr := f.call(t, "draw_flowchart", map[string]any{"space": "homelab", "path": "arch.excalidraw.md", "mermaid": "flowchart LR\n  d[New]\n"}); !isErr {
		t.Fatalf("missing base accepted: %s", txt)
	}
	if !bytes.Equal(after, f.readFile(t, r.Path)) {
		t.Fatal("a refused write changed the file")
	}
}

func TestDrawFlowchartRefusals(t *testing.T) {
	f := newFixture(t)
	refused := func(args map[string]any, want string) {
		t.Helper()
		if args["space"] == nil {
			args["space"] = "homelab"
		}
		txt, isErr := f.call(t, "draw_flowchart", args)
		if !isErr || !strings.Contains(txt, want) {
			t.Fatalf("%v: want error %q, got %v %s", args, want, isErr, txt)
		}
	}
	refused(map[string]any{"path": "s.excalidraw.md", "mermaid": "sequenceDiagram\n  A->>B: hi\n"},
		"only flowcharts can be drawn for now; write a mermaid block in the note instead")
	refused(map[string]any{"path": "s.excalidraw.md", "mermaid": "flowchart LR\n  a[oops --> b\n"}, "mermaid: line")
	refused(map[string]any{"path": "s.md", "mermaid": archMermaid}, ".excalidraw.md")
	refused(map[string]any{"space": "secret", "path": "s.excalidraw.md", "mermaid": archMermaid}, "not scoped")
	if _, err := os.Stat(filepath.Join(f.dir, "homelab", "s.excalidraw.md")); err == nil {
		t.Fatal("a refused draw created the file")
	}

	r := f.draw(t, map[string]any{"path": "arch.excalidraw.md", "mermaid": archMermaid})
	before := f.readFile(t, r.Path)
	refused(map[string]any{"path": "arch.excalidraw.md", "mermaid": archMermaid, "base": r.Base, "remove": []string{"nope"}}, "unknown ids: nope")
	if !bytes.Equal(before, f.readFile(t, r.Path)) {
		t.Fatal("an unknown removal changed the file")
	}

	owner := auth.Identity{UserID: "u1", Owner: true}
	ro, err := f.as.CreateAgentToken(context.Background(), owner, "reader", []string{"homelab"}, false)
	if err != nil {
		t.Fatal(err)
	}
	f.token = ro.Secret
	refused(map[string]any{"path": "ro.excalidraw.md", "mermaid": archMermaid}, "cannot write")
	if txt, isErr := f.call(t, "add_drawing_image", map[string]any{"drawing": r.Path, "data": "x", "base": r.Base}); !isErr || !strings.Contains(txt, "cannot write") {
		t.Fatalf("read-only add accepted: %v %s", isErr, txt)
	}
}

func TestDrawFlowchartUnreadableUntouched(t *testing.T) {
	f := newFixture(t)
	f.dropNote(t, "homelab/bad.excalidraw.md", "not a scene\n")
	before := f.readFile(t, "homelab/bad.excalidraw.md")
	txt, isErr := f.call(t, "draw_flowchart", map[string]any{
		"space": "homelab", "path": "bad.excalidraw.md", "mermaid": archMermaid, "base": drawing.Hash(before),
	})
	if !isErr || txt != "the drawing on disk could not be read; open it in yana first" {
		t.Fatalf("unreadable drawing written: %v %s", isErr, txt)
	}
	if !bytes.Equal(before, f.readFile(t, "homelab/bad.excalidraw.md")) {
		t.Fatal("the unreadable drawing changed")
	}
}

func TestDrawingWriteReachesWatchers(t *testing.T) {
	f := newFixture(t)
	r := f.draw(t, map[string]any{"path": "arch.excalidraw.md", "mermaid": archMermaid})
	events := make(chan reconcile.Event, 16)
	unsub := f.rec.Subscribe(func(ev reconcile.Event) {
		if ev.Kind == reconcile.EventChanged {
			select {
			case events <- ev:
			default:
			}
		}
	})
	defer unsub()
	r2 := f.draw(t, map[string]any{"path": "arch.excalidraw.md", "mermaid": "flowchart LR\n  a --> c[Server]\n", "base": r.Base})
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Path == r.Path && ev.Hash == r2.Base {
				return
			}
		case <-deadline:
			t.Fatal("no change notice for the drawing write")
		}
	}
}

type fakeGit struct {
	mu    sync.Mutex
	calls [][2]string
}

func (g *fakeGit) Author(path, author string) {
	g.mu.Lock()
	g.calls = append(g.calls, [2]string{path, author})
	g.mu.Unlock()
}

func TestDrawingGitAuthor(t *testing.T) {
	f := newFixture(t)
	g := &fakeGit{}
	f.h.Git = g
	r := f.draw(t, map[string]any{"path": "arch.excalidraw.md", "mermaid": archMermaid})
	f.draw(t, map[string]any{"path": "arch.excalidraw.md", "mermaid": "flowchart LR\n  a --> c[Server]\n", "base": r.Base, "agent_label": "night"})
	g.mu.Lock()
	defer g.mu.Unlock()
	want := [][2]string{{r.Path, "agent:docs"}, {r.Path, "agent:night"}}
	if fmt.Sprint(g.calls) != fmt.Sprint(want) {
		t.Fatalf("git authors %v, want %v", g.calls, want)
	}
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 40, 20))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAddDrawingImage(t *testing.T) {
	f := newFixture(t)
	r := f.draw(t, map[string]any{"path": "arch.excalidraw.md", "mermaid": archMermaid})
	pic := base64.StdEncoding.EncodeToString(pngBytes(t))

	txt, isErr := f.call(t, "add_drawing_image", map[string]any{"drawing": r.Path, "data": "data:image/png;base64," + pic, "near": "a", "base": r.Base})
	var res struct{ Path, Base string }
	if isErr || json.Unmarshal([]byte(txt), &res) != nil {
		t.Fatalf("add failed: %s", txt)
	}
	if res.Path != r.Path || res.Base != drawing.Hash(f.readFile(t, r.Path)) {
		t.Fatalf("bad result: %s", txt)
	}
	blocks, isErr := f.callAll(t, "read_note", map[string]any{"note": r.Path})
	if isErr || len(blocks) != 2 || blocks[1]["type"] != "image" || blocks[1]["mimeType"] != "image/png" || blocks[1]["data"] != pic {
		t.Fatalf("read_note blocks wrong: %v", blocks)
	}

	// From a file already in the space.
	if err := os.MkdirAll(filepath.Join(f.dir, "homelab", "_assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "homelab", "_assets", "pic.png"), pngBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	txt, isErr = f.call(t, "add_drawing_image", map[string]any{"drawing": r.ID, "asset": "_assets/pic.png", "base": res.Base})
	if isErr {
		t.Fatalf("add from asset failed: %s", txt)
	}
	_ = json.Unmarshal([]byte(txt), &res)

	// A huge width is clamped.
	txt, isErr = f.call(t, "add_drawing_image", map[string]any{"drawing": r.ID, "data": pic, "width": 1e9, "base": res.Base})
	if isErr {
		t.Fatalf("add with width failed: %s", txt)
	}
	_ = json.Unmarshal([]byte(txt), &res)
	_, scene, _ := drawing.Parse(frontmatter.Parse(f.readFile(t, r.Path)).Body)
	v, _ := flowchart.Read(scene)
	if last := v.Items[len(v.Items)-1]; last.Type != "image" || last.W != 4000 {
		t.Fatalf("width not clamped: %+v", last)
	}
	before := f.readFile(t, r.Path)

	refused := func(args map[string]any, want string) {
		t.Helper()
		args["drawing"], args["base"] = r.Path, res.Base
		txt, isErr := f.call(t, "add_drawing_image", args)
		if !isErr || !strings.Contains(txt, want) {
			t.Fatalf("%v: want error %q, got %v %s", args, want, isErr, txt)
		}
	}
	webp := base64.StdEncoding.EncodeToString([]byte("RIFF\x1a\x00\x00\x00WEBPVP8 \x0e\x00\x00\x00"))
	refused(map[string]any{"data": webp}, "only PNG, JPEG and GIF images can be added")
	refused(map[string]any{"asset": "../secret/x.png"}, "not allowed")
	refused(map[string]any{"asset": "_assets/missing.png"}, "no file")
	refused(map[string]any{"data": pic, "asset": "_assets/pic.png"}, "one of asset or data")
	refused(map[string]any{"data": pic, "near": "zzz"}, "unknown ids: zzz")
	if txt, isErr := f.call(t, "add_drawing_image", map[string]any{"drawing": r.Path, "data": pic, "base": r.Base}); !isErr ||
		txt != "the drawing changed since you read it; read it again" {
		t.Fatalf("stale base accepted: %v %s", isErr, txt)
	}
	if !bytes.Equal(before, f.readFile(t, r.Path)) {
		t.Fatal("a refused add changed the file")
	}
}

// TestDrawingContentCaps: past five pictures, read_note marks the rest
// "(not included)" on their layout lines instead of sending them.
func TestDrawingContentCaps(t *testing.T) {
	scene := []byte(drawing.EmptyScene)
	for i := 1; i <= 6; i++ {
		var buf bytes.Buffer
		if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 10*i, 10))); err != nil {
			t.Fatal(err)
		}
		var err error
		if scene, err = flowchart.AddImage(scene, buf.Bytes(), "", 0, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	v, err := flowchart.Read(scene)
	if err != nil {
		t.Fatal(err)
	}
	content, images := drawingContent(v, scene)
	if len(images) != 5 || strings.Count(content, " image (not included)\n") != 1 {
		t.Fatalf("got %d images:\n%s", len(images), content)
	}
}

// browserJSON marshals v compactly with object keys in reverse order, as a
// serializer other than Go's would.
func browserJSON(v any) string {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(keys)))
		parts := make([]string, len(keys))
		for i, k := range keys {
			kb, _ := json.Marshal(k)
			parts[i] = string(kb) + ":" + browserJSON(x[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = browserJSON(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func TestDrawFlowchartNoOpKeepsFile(t *testing.T) {
	f := newFixture(t)
	r := f.draw(t, map[string]any{"path": "arch.excalidraw.md", "mermaid": "flowchart LR\n  subgraph G [Group]\n    a[Router] -->|uplink| b[Switch]\n  end\n  b --> c\n"})
	// the browser saves it with its own key order and no files key
	fm := frontmatter.Parse(f.readFile(t, r.Path))
	_, scene, err := drawing.Parse(fm.Body)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(scene, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "files")
	saved := append(append([]byte{}, fm.Head...), drawing.Encode([]byte(browserJSON(m)))...)
	if err := os.WriteFile(filepath.Join(f.dir, filepath.FromSlash(r.Path)), saved, 0o644); err != nil {
		t.Fatal(err)
	}

	txt, isErr := f.call(t, "read_note", map[string]any{"note": r.Path})
	var read struct{ Base, Content string }
	if isErr || json.Unmarshal([]byte(txt), &read) != nil {
		t.Fatalf("read failed: %s", txt)
	}
	mermaid := read.Content[strings.Index(read.Content, "flowchart"):strings.Index(read.Content, "\nlayout:")]
	r2 := f.draw(t, map[string]any{"path": r.Path[len("homelab/"):], "mermaid": mermaid, "base": read.Base})
	if !bytes.Equal(f.readFile(t, r.Path), saved) {
		t.Fatal("a no-op resend rewrote the file")
	}
	for k, n := range r2.Stats {
		if n != 0 {
			t.Errorf("stats %s = %d", k, n)
		}
	}
	if r2.Base != read.Base {
		t.Errorf("base changed")
	}
}
