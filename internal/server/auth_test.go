package server

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

	"github.com/coder/websocket"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/madeofpendletonwool/yana/internal/auth"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/rt"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/spaces"
)

// authFixture is a full stack with accounts on: root, index, scanner,
// reconciliation loop (fast timings, real watcher), auth service, relay
// with token verification, and the HTTP server.
type authFixture struct {
	dir  string
	root *pathsafe.Root
	db   *index.DB
	sc   *scanner.Scanner
	rec  *reconcile.Reconciler
	as   *auth.Service
	hub  *rt.Hub
	srv  *Server
	ts   *httptest.Server
	log  *slog.Logger
}

func newAuthFixture(t *testing.T) *authFixture {
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
	hub := rt.New(rec, as, rt.Options{
		Verify: func(token string) (rt.Identity, error) {
			id, err := as.VerifyAccess(token)
			if err != nil {
				return rt.Identity{}, err
			}
			return rt.Identity{UserID: id.UserID, Username: id.Username, Owner: id.Owner, SessionID: id.SessionID}, nil
		},
	}, log)
	t.Cleanup(hub.Close)
	// A .space.yml edit severs open subscriptions through this hook,
	// exactly as main.go wires it.
	rec.SetOnSpaceMembersChanged(func(space string) {
		hub.RecheckSpace(context.Background(), space)
	})
	as.OnSessionRevoked(hub.KickSession)

	f := &authFixture{dir: dir, root: root, db: db, sc: sc, rec: rec, as: as, hub: hub, log: log}
	f.srv = New(Deps{DB: db, Root: root, Log: log, Version: "test", Sync: rec, RT: hub, Scanner: sc, Auth: as})
	f.srv.SetReady(true)
	f.ts = httptest.NewServer(f.srv)
	t.Cleanup(f.ts.Close)
	return f
}

// writeNote drops a note file and indexes it, returning its id.
func (f *authFixture) writeNote(t *testing.T, rel, body string) string {
	t.Helper()
	id := scanner.NewID(time.Now())
	abs := filepath.Join(f.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	note := fmt.Sprintf("---\nid: %s\ncreated: 2026-09-13T00:00:00Z\n---\n%s", id, body)
	if err := os.WriteFile(abs, []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.sc.ScanOne(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	return id
}

// writeSpaceFile writes a space's .space.yml by hand, the way an admin
// with a shell would.
func (f *authFixture) writeSpaceFile(t *testing.T, space string, spec spaces.Spec) {
	t.Helper()
	abs := filepath.Join(f.dir, space, spaces.FileName)
	if err := os.WriteFile(abs, spaces.Render(spec), 0o644); err != nil {
		t.Fatal(err)
	}
}

// account creates a user (owner when first) and returns the session id
// and a ready Authorization header value.
func (f *authFixture) account(t *testing.T, username, password string) (sessionID, authHeader string) {
	t.Helper()
	ctx := context.Background()
	if !f.as.HasAccounts(ctx) {
		if _, _, err := f.as.Setup(ctx, username, password, "test"); err != nil {
			t.Fatal(err)
		}
	} else {
		ident := f.ownerIdent(t)
		if _, err := f.as.CreateUser(ctx, ident, username, password); err != nil {
			t.Fatal(err)
		}
	}
	return f.accountLogin(t, username, password)
}

// ownerIdent mints a verified identity for the global owner.
func (f *authFixture) ownerIdent(t *testing.T) auth.Identity {
	t.Helper()
	_, tok, err := f.as.Login(context.Background(), "owner", "owner-password", "test")
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.as.VerifyAccess(tok.Access)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// --- HTTP helpers -----------------------------------------------------------

func doGet(t *testing.T, ts *httptest.Server, path, auth string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest("GET", ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	return resp.StatusCode, out
}

func doPost(t *testing.T, ts *httptest.Server, method, path, auth string, payload any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	return resp.StatusCode, out
}

// --- the flows -----------------------------------------------------------------

func TestAuthFirstRunFlow(t *testing.T) {
	f := newAuthFixture(t)

	// Before setup: the API reports what is missing, health stays up.
	if code, body := doGet(t, f.ts, "/api/tree", ""); code != http.StatusUnauthorized || body["setup_required"] != true {
		t.Fatalf("pre-setup /api/tree: %d %v", code, body)
	}
	if code, _ := doGet(t, f.ts, "/healthz", ""); code != http.StatusOK {
		t.Fatal("healthz must stay public")
	}
	if code, body := doGet(t, f.ts, "/api/auth/state", ""); code != http.StatusOK || body["setup_required"] != true {
		t.Fatalf("auth state: %d %v", code, body)
	}

	// No default credentials: a guessable login fails.
	if code, _ := doPost(t, f.ts, "POST", "/api/auth/login", "", map[string]string{"username": "admin", "password": "admin"}); code != http.StatusUnauthorized {
		t.Fatal("guessed login accepted")
	}

	// Weak passwords and bad usernames are refused.
	if code, _ := doPost(t, f.ts, "POST", "/api/auth/setup", "", map[string]string{"username": "a", "password": "longenough1"}); code != http.StatusBadRequest {
		t.Fatal("short username accepted at setup")
	}
	if code, _ := doPost(t, f.ts, "POST", "/api/auth/setup", "", map[string]string{"username": "owner", "password": "short"}); code != http.StatusBadRequest {
		t.Fatal("short password accepted at setup")
	}

	if code, body := doPost(t, f.ts, "POST", "/api/auth/setup", "", map[string]string{"username": "owner", "password": "owner-password"}); code != http.StatusCreated {
		t.Fatalf("setup: %d %v", code, body)
	}
	// A second setup is refused.
	if code, _ := doPost(t, f.ts, "POST", "/api/auth/setup", "", map[string]string{"username": "other", "password": "password123"}); code != http.StatusBadRequest {
		t.Fatal("second setup allowed")
	}
	if _, body := doGet(t, f.ts, "/api/auth/state", ""); body["setup_required"] == true {
		t.Fatal("state still requires setup")
	}

	// Login returns tokens; the API answers with them.
	if code, _ := doPost(t, f.ts, "POST", "/api/auth/login", "", map[string]string{"username": "owner", "password": "wrong-password"}); code != http.StatusUnauthorized {
		t.Fatal("wrong password accepted")
	}
	code, body := doPost(t, f.ts, "POST", "/api/auth/login", "", map[string]string{"username": "owner", "password": "owner-password"})
	if code != http.StatusOK {
		t.Fatalf("login: %d %v", code, body)
	}
	tok := body["tokens"].(map[string]any)
	access := tok["access_token"].(string)

	// Refresh mints a new access token.
	code, body = doPost(t, f.ts, "POST", "/api/auth/refresh", "", map[string]string{"refresh_token": tok["refresh_token"].(string)})
	if code != http.StatusOK {
		t.Fatalf("refresh: %d %v", code, body)
	}
	access2 := body["tokens"].(map[string]any)["access_token"].(string)
	if access == access2 {
		t.Fatal("refresh returned the same access token")
	}

	// The API answers only with a valid token.
	if code, _ := doGet(t, f.ts, "/api/tree", ""); code != http.StatusUnauthorized {
		t.Fatal("missing token accepted")
	}
	if code, _ := doGet(t, f.ts, "/api/tree", "Bearer "+access2); code != http.StatusOK {
		t.Fatal("valid token refused")
	}
	if code, _ := doGet(t, f.ts, "/api/tree", "Bearer v1.forged.junk"); code != http.StatusUnauthorized {
		t.Fatal("forged token accepted")
	}

	// Sessions are listed and revocable through the API.
	_, hdr := f.accountLogin(t, "owner", "owner-password")
	if code, body := doGet(t, f.ts, "/api/auth/sessions", hdr); code != http.StatusOK {
		t.Fatalf("sessions: %d %v", code, body)
	}
}

// --- authorization across spaces ---------------------------------------------

// authzWorld builds: owner (global), sam editor of home, eve viewer of
// home, and nobody of work. home and work each hold one note.
type authzWorld struct {
	ownerHdr, samHdr, eveHdr string
	homeID, workID, rootID   string
	homeAsset                string
}

func (f *authFixture) buildWorld(t *testing.T) *authzWorld {
	t.Helper()
	ctx := context.Background()
	w := &authzWorld{}
	if _, _, err := f.as.Setup(ctx, "owner", "owner-password", "t"); err != nil {
		t.Fatal(err)
	}
	_, ownerTok, _ := f.as.Login(ctx, "owner", "owner-password", "t")
	ownerID, err := f.as.VerifyAccess(ownerTok.Access)
	if err != nil {
		t.Fatal(err)
	}
	sam, err := f.as.CreateUser(ctx, ownerID, "sam", "sam-password1")
	if err != nil {
		t.Fatal(err)
	}
	eve, err := f.as.CreateUser(ctx, ownerID, "eve", "eve-password1")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(f.dir, "home", "_assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.dir, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.writeSpaceFile(t, "home", spaces.Spec{Name: "home", Members: []spaces.Member{
		{User: sam.ID, Role: spaces.RoleEditor},
		{User: eve.ID, Role: spaces.RoleViewer},
	}})
	// work has no .space.yml: owner-only.
	w.homeID = f.writeNote(t, "home/home-secret.md", "the raspberry jam recipe\n")
	w.workID = f.writeNote(t, "work/work-secret.md", "quarterly numbers nobody should see\n")
	w.rootID = f.writeNote(t, "loose.md", "a note loose in the root\n")
	w.homeAsset = "home/_assets/diagram.png"
	if err := os.WriteFile(filepath.Join(f.dir, filepath.FromSlash(w.homeAsset)), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sc.Scan(ctx); err != nil {
		t.Fatal(err)
	}

	_, w.ownerHdr = f.accountLogin(t, "owner", "owner-password")
	_, w.samHdr = f.accountLogin(t, "sam", "sam-password1")
	_, w.eveHdr = f.accountLogin(t, "eve", "eve-password1")
	return w
}

func (f *authFixture) accountLogin(t *testing.T, username, password string) (string, string) {
	t.Helper()
	_, tok, err := f.as.Login(context.Background(), username, password, "test")
	if err != nil {
		t.Fatal(err)
	}
	return tok.SessionID, "Bearer " + tok.Access
}

func TestSpaceBoundariesAtREST(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)

	cases := []struct {
		name string
		hdr  string
		path string
		want int
	}{
		{"owner reads home", w.ownerHdr, "/api/notes/" + w.homeID, 200},
		{"owner reads work", w.ownerHdr, "/api/notes/" + w.workID, 200},
		{"editor reads own space", w.samHdr, "/api/notes/" + w.homeID, 200},
		{"viewer reads own space", w.eveHdr, "/api/notes/" + w.homeID, 200},
		{"editor cannot read other space", w.samHdr, "/api/notes/" + w.workID, 404},
		{"viewer cannot read other space", w.eveHdr, "/api/notes/" + w.workID, 404},
		{"editor cannot read root space", w.samHdr, "/api/notes/" + w.rootID, 404},
		{"owner reads root space", w.ownerHdr, "/api/notes/" + w.rootID, 200},
	}
	for _, tc := range cases {
		if code, _ := doGet(t, f.ts, tc.path, tc.hdr); code != tc.want {
			t.Errorf("%s: got %d want %d", tc.name, code, tc.want)
		}
	}

	// The tree only shows member spaces.
	_, tree := doGet(t, f.ts, "/api/tree", w.samHdr)
	seen := map[string]bool{}
	for _, s := range tree["spaces"].([]any) {
		seen[s.(map[string]any)["name"].(string)] = true
	}
	if seen["work"] || seen[""] {
		t.Fatalf("sam's tree leaks spaces: %v", seen)
	}
	if !seen["home"] {
		t.Fatalf("sam's tree missing home: %v", seen)
	}
	_, ownerTree := doGet(t, f.ts, "/api/tree", w.ownerHdr)
	if len(ownerTree["spaces"].([]any)) < 3 {
		t.Fatalf("owner tree too small: %v", ownerTree)
	}

	// Search never leaks across boundaries: title and preview.
	_, hits := doGet(t, f.ts, "/api/search?q=quarterly", w.samHdr)
	if n := len(hits["hits"].([]any)); n != 0 {
		t.Fatalf("sam's search leaked work notes: %v", hits)
	}
	_, hits = doGet(t, f.ts, "/api/search?q=quarterly", w.ownerHdr)
	if n := len(hits["hits"].([]any)); n != 1 {
		t.Fatalf("owner search should find the work note: %v", hits)
	}
	_, hits = doGet(t, f.ts, "/api/search?q=raspberry", w.eveHdr)
	if n := len(hits["hits"].([]any)); n != 1 {
		t.Fatalf("viewer search in own space: %v", hits)
	}
	// Explicit requests for a foreign space are refused as missing.
	if code, _ := doGet(t, f.ts, "/api/search?q=quarterly&space=work", w.samHdr); code != http.StatusNotFound {
		t.Fatalf("foreign space search: %d", code)
	}
	// The tree endpoint for a foreign space too.
	if code, _ := doGet(t, f.ts, "/api/tree?space=work", w.samHdr); code != http.StatusNotFound {
		t.Fatalf("foreign space tree: %d", code)
	}

	// Assets live behind the same boundary.
	if code, _ := doGet(t, f.ts, "/api/files/"+w.homeAsset, w.samHdr); code != http.StatusOK {
		t.Fatal("member cannot read an asset of their own space")
	}

	// Writes: viewer cannot create or move; editor cannot move into a
	// foreign space.
	if code, _ := doPost(t, f.ts, "POST", "/api/notes", w.eveHdr, map[string]string{"path": "home/new.md", "content": "x"}); code != http.StatusForbidden {
		t.Fatalf("viewer create: %d", code)
	}
	if code, _ := doPost(t, f.ts, "POST", "/api/notes", w.samHdr, map[string]string{"path": "work/new.md", "content": "x"}); code != http.StatusNotFound {
		t.Fatalf("editor create in foreign space: %d", code)
	}
	if code, _ := doPost(t, f.ts, "POST", "/api/notes", w.samHdr, map[string]string{"path": "home/new.md", "content": "hello"}); code != http.StatusCreated {
		t.Fatalf("editor create in own space: %d", code)
	}
	if code, _ := doPost(t, f.ts, "POST", "/api/notes/"+w.homeID+"/move", w.eveHdr, map[string]string{"path": "home/moved.md"}); code != http.StatusForbidden {
		t.Fatalf("viewer move: %d", code)
	}
	if code, _ := doPost(t, f.ts, "POST", "/api/notes/"+w.homeID+"/move", w.samHdr, map[string]string{"path": "work/moved.md"}); code != http.StatusNotFound {
		t.Fatalf("cross-space move without destination access: %d", code)
	}
	if code, _ := doPost(t, f.ts, "POST", "/api/notes/"+w.homeID+"/move", w.samHdr, map[string]string{"path": "home/renamed.md"}); code != http.StatusOK {
		t.Fatalf("same-space move by editor: %d", code)
	}

	// Backlinks and the unresolved report respect the same line.
	if code, _ := doGet(t, f.ts, "/api/notes/"+w.homeID+"/backlinks", w.samHdr); code != http.StatusOK {
		t.Fatalf("backlinks: %d", code)
	}
	if code, _ := doGet(t, f.ts, "/api/notes/"+w.workID+"/backlinks", w.samHdr); code != http.StatusNotFound {
		t.Fatalf("foreign backlinks: %d", code)
	}
	if code, _ := doGet(t, f.ts, "/api/links/unresolved?space=work", w.samHdr); code != http.StatusNotFound {
		t.Fatalf("foreign unresolved: %d", code)
	}
	// Snapshots are for the global owner.
	if code, _ := doPost(t, f.ts, "POST", "/api/git/snapshot", w.samHdr, nil); code == http.StatusOK {
		t.Fatal("non-owner ran a snapshot")
	}
}

func TestSpaceCRUD(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)

	// The owner creates a space; .space.yml names them owner.
	if code, body := doPost(t, f.ts, "POST", "/api/spaces", w.ownerHdr, map[string]string{"name": "garden"}); code != http.StatusCreated {
		t.Fatalf("create space: %d %v", code, body)
	}
	spec, err := spaces.Parse(mustRead(t, filepath.Join(f.dir, "garden", ".space.yml")))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "garden" || len(spec.Members) != 1 || spec.Members[0].Role != spaces.RoleOwner {
		t.Fatalf("generated .space.yml: %+v", spec)
	}
	// A bad name is a bad name.
	if code, _ := doPost(t, f.ts, "POST", "/api/spaces", w.ownerHdr, map[string]string{"name": "a/b"}); code != http.StatusBadRequest {
		t.Fatal("path-like space name accepted")
	}
	if code, _ := doPost(t, f.ts, "POST", "/api/spaces", w.ownerHdr, map[string]string{"name": "home"}); code != http.StatusConflict {
		t.Fatal("duplicate space accepted")
	}

	// Detail shows the role.
	if code, body := doGet(t, f.ts, "/api/spaces/home", w.samHdr); code != 200 || body["role"] != spaces.RoleEditor {
		t.Fatalf("space detail: %d %v", code, body)
	}
	if code, _ := doGet(t, f.ts, "/api/spaces/work", w.samHdr); code != http.StatusNotFound {
		t.Fatal("foreign space detail leaked")
	}

	// Only space owners may rewrite membership; an editor cannot.
	if code, _ := doPost(t, f.ts, "PATCH", "/api/spaces/home", w.samHdr, map[string]any{"members": []map[string]string{}}); code != http.StatusForbidden {
		t.Fatalf("editor rewrote membership: %d", code)
	}
	// The owner adds a member by username (hand-edit-friendly).
	if code, _ := doPost(t, f.ts, "PATCH", "/api/spaces/home", w.ownerHdr, map[string]any{
		"name": "home", "members": []map[string]string{{"user": "sam", "role": "editor"}, {"user": "eve", "role": "viewer"}},
	}); code != http.StatusOK {
		t.Fatal("owner could not rewrite membership")
	}

	// A non-empty space refuses deletion.
	if code, _ := doPost(t, f.ts, "DELETE", "/api/spaces/home", w.ownerHdr, nil); code != http.StatusConflict {
		t.Fatalf("non-empty space deleted: %d", code)
	}
	// An empty one goes.
	doPost(t, f.ts, "POST", "/api/spaces", w.ownerHdr, map[string]string{"name": "scratch"})
	if code, _ := doPost(t, f.ts, "DELETE", "/api/spaces/scratch", w.ownerHdr, nil); code != http.StatusOK {
		t.Fatal("empty space not deleted")
	}
	if _, err := os.Stat(filepath.Join(f.dir, "scratch")); !os.IsNotExist(err) {
		t.Fatal("scratch directory still on disk")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// --- the WebSocket layer ---------------------------------------------------------

func dialWS(t *testing.T, ts *httptest.Server, token string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	if token != "" {
		u += "?token=" + token
	}
	return websocket.Dial(ctx, u, nil)
}

func wsSend(t *testing.T, ctx context.Context, ws *websocket.Conn, msg any) {
	t.Helper()
	w, err := ws.Writer(ctx, websocket.MessageBinary)
	if err != nil {
		t.Fatal(err)
	}
	if err := msgpack.NewEncoder(w).Encode(msg); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

type wsReply struct {
	Type   string `msgpack:"t"`
	Note   string `msgpack:"n"`
	Update []byte `msgpack:"u"`
	Code   string `msgpack:"c"`
	Reason string `msgpack:"r"`
}

// subscribe sends a sub frame and returns the next reply.
func subscribe(t *testing.T, ctx context.Context, ws *websocket.Conn, noteID, author string) wsReply {
	t.Helper()
	wsSend(t, ctx, ws, map[string]any{"t": "sub", "n": noteID, "a": author})
	var reply wsReply
	if err := wsRead(ctx, ws, &reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

func TestWebSocketRequiresToken(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)

	if _, resp, err := dialWS(t, f.ts, ""); err == nil {
		t.Fatal("anonymous websocket accepted")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous upgrade status: %v", resp)
	}
	if _, resp, err := dialWS(t, f.ts, "v1.forged.junk"); err == nil {
		t.Fatal("forged token accepted at upgrade")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged upgrade status: %v", resp)
	}
	// A valid token connects.
	_, tok, _ := f.as.Login(context.Background(), "sam", "sam-password1", "test")
	ws, _, err := dialWS(t, f.ts, tok.Access)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "bye")
	_ = w
}

func TestWebSocketSubscribeBoundaries(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, samTok, _ := f.as.Login(context.Background(), "sam", "sam-password1", "test")
	_, eveTok, _ := f.as.Login(context.Background(), "eve", "eve-password1", "test")

	samWS, _, err := dialWS(t, f.ts, samTok.Access)
	if err != nil {
		t.Fatal(err)
	}
	defer samWS.Close(websocket.StatusNormalClosure, "bye")

	// A member subscribes and gets the snapshot.
	if reply := subscribe(t, ctx, samWS, w.homeID, "user:sam"); reply.Type != "subd" {
		t.Fatalf("member subscribe: %+v", reply)
	}
	// A foreign note is refused — verified at the WebSocket layer.
	if reply := subscribe(t, ctx, samWS, w.workID, "user:sam"); reply.Type != "err" || reply.Code != "forbidden" {
		t.Fatalf("foreign subscribe: %+v", reply)
	}
	// The author cannot be spoofed into access: the server names the
	// author from the token, not the frame.
	if reply := subscribe(t, ctx, samWS, w.workID, "user:owner"); reply.Type != "err" || reply.Code != "forbidden" {
		t.Fatalf("spoofed author subscribe: %+v", reply)
	}

	// A viewer may subscribe read-only and may not push updates.
	eveWS, _, err := dialWS(t, f.ts, eveTok.Access)
	if err != nil {
		t.Fatal(err)
	}
	defer eveWS.Close(websocket.StatusNormalClosure, "bye")
	if reply := subscribe(t, ctx, eveWS, w.homeID, "user:eve"); reply.Type != "subd" {
		t.Fatalf("viewer subscribe: %+v", reply)
	}
	wsSend(t, ctx, eveWS, map[string]any{"t": "upd", "n": w.homeID, "u": []byte{1, 2, 3}, "a": "user:eve"})
	var reply wsReply
	if err := wsRead(ctx, eveWS, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Type != "err" || reply.Code != "forbidden" {
		t.Fatalf("viewer update: %+v", reply)
	}
	// Updates without a subscription are refused too.
	wsSend(t, ctx, samWS, map[string]any{"t": "upd", "n": w.rootID, "u": []byte{1, 2, 3}, "a": "user:sam"})
	if err := wsRead(ctx, samWS, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Type != "err" || reply.Code != "forbidden" {
		t.Fatalf("unsubscribed update: %+v", reply)
	}
}

// TestMembershipEditSeversSubscription is the Phase 4 acceptance: a
// hand edit of .space.yml severs access within one watcher cycle,
// including open subscriptions.
func TestMembershipEditSeversSubscription(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, samTok, _ := f.as.Login(context.Background(), "sam", "sam-password1", "test")
	ws, _, err := dialWS(t, f.ts, samTok.Access)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "bye")
	if reply := subscribe(t, ctx, ws, w.homeID, "user:sam"); reply.Type != "subd" {
		t.Fatalf("subscribe: %+v", reply)
	}

	// Remove sam from the space by hand.
	owner, err := f.db.GetUserByName(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	f.writeSpaceFile(t, "home", spaces.Spec{Name: "home", Members: []spaces.Member{
		{User: owner.ID, Role: spaces.RoleOwner},
	}})

	// Within one watcher cycle (debounce + process) the open
	// subscription is severed with a forbidden error.
	deadline := time.Now().Add(10 * time.Second)
	var severed bool
	for time.Now().Before(deadline) {
		var reply wsReply
		readCtx, cancel2 := context.WithTimeout(ctx, 200*time.Millisecond)
		if err := wsRead(readCtx, ws, &reply); err == nil && reply.Type == "err" && reply.Code == "forbidden" {
			severed = true
			cancel2()
			break
		}
		cancel2()
	}
	if !severed {
		t.Fatal("open subscription was not severed after the membership edit")
	}

	// And re-subscribing is refused.
	if reply := subscribe(t, ctx, ws, w.homeID, "user:sam"); reply.Type != "err" || reply.Code != "forbidden" {
		t.Fatalf("re-subscribe after removal: %+v", reply)
	}
}

// TestSessionRevocationClosesSockets: revoking a session closes its
// live connections.
func TestSessionRevocationClosesSockets(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID, tok := f.accountLogin(t, "sam", "sam-password1")
	ws, _, err := dialWS(t, f.ts, strings.TrimPrefix(tok, "Bearer "))
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "bye")
	if reply := subscribe(t, ctx, ws, w.homeID, "user:sam"); reply.Type != "subd" {
		t.Fatalf("subscribe: %+v", reply)
	}

	// The global owner revokes sam's session; the socket closes.
	if err := f.as.RevokeSession(context.Background(), f.ownerIdent(t), sessionID); err != nil {
		t.Fatal(err)
	}
	_, _, err = ws.Reader(ctx)
	if err == nil {
		t.Fatal("connection stayed open after session revocation")
	}
}
