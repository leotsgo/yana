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
	"time"

	"github.com/madeofpendletonwool/yana/internal/spaces"
)

func TestAgentTokenEndpoints(t *testing.T) {
	f := newAuthFixture(t)
	_, ownerAuth := f.account(t, "owner", "owner-password")
	f.account(t, "sam", "sam-password")
	_, samAuth := f.accountLogin(t, "sam", "sam-password")

	// Only the owner mints tokens.
	status, body := doPost(t, f.ts, "POST", "/api/agents", samAuth,
		map[string]any{"label": "docs", "spaces": []string{"homelab"}, "can_write": true})
	if status != http.StatusForbidden {
		t.Fatalf("non-owner minted a token: %d %v", status, body)
	}
	status, body = doPost(t, f.ts, "POST", "/api/agents", ownerAuth,
		map[string]any{"label": "docs", "spaces": []string{"homelab"}, "can_write": true})
	if status != http.StatusCreated {
		t.Fatalf("mint failed: %d %v", status, body)
	}
	secret, _ := body["token"].(string)
	id, _ := body["id"].(string)
	if secret == "" || !strings.HasPrefix(secret, "ya_") || id == "" {
		t.Fatalf("mint answer is wrong: %v", body)
	}

	// Listing shows the token; the secret never comes back.
	status, body = doGet(t, f.ts, "/api/agents", ownerAuth)
	if status != http.StatusOK {
		t.Fatalf("list failed: %d", status)
	}
	agents, _ := body["agents"].([]any)
	if len(agents) != 1 {
		t.Fatalf("listing shows %d tokens", len(agents))
	}
	row, _ := agents[0].(map[string]any)
	if _, has := row["token"]; has {
		t.Fatal("listing leaks the secret")
	}
	if row["label"] != "docs" {
		t.Fatalf("listing label wrong: %v", row)
	}

	// The minted token works against /mcp.
	rpc := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
	raw, _ := json.Marshal(rpc)
	req, _ := http.NewRequest("POST", f.ts.URL+"/mcp", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mcp with fresh token: %d", resp.StatusCode)
	}

	// A live label is taken; a revoked one is free again.
	status, _ = doPost(t, f.ts, "POST", "/api/agents", ownerAuth,
		map[string]any{"label": "docs", "spaces": []string{"homelab"}})
	if status != http.StatusConflict {
		t.Fatalf("duplicate live label: %d", status)
	}

	// Revocation is immediate and the token stops working.
	status, _ = doPost(t, f.ts, "DELETE", "/api/agents/"+id, ownerAuth, nil)
	if status != http.StatusOK {
		t.Fatalf("revoke failed: %d", status)
	}
	req, _ = http.NewRequest("POST", f.ts.URL+"/mcp", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err = f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token still works: %d", resp.StatusCode)
	}
	status, _ = doPost(t, f.ts, "DELETE", "/api/agents/"+id, ownerAuth, nil)
	if status != http.StatusNotFound {
		t.Fatalf("double revoke: %d", status)
	}
	status, _ = doPost(t, f.ts, "POST", "/api/agents", ownerAuth,
		map[string]any{"label": "docs", "spaces": []string{"homelab"}})
	if status != http.StatusCreated {
		t.Fatalf("label not free after revocation: %d", status)
	}
}

func TestConventionsEndpoint(t *testing.T) {
	f := newAuthFixture(t)
	f.account(t, "owner", "owner-password")
	f.account(t, "sam", "sam-password")

	f.writeNote(t, "homelab/network.md", "# Network\n\nConfig.\n")
	f.writeSpaceFile(t, "homelab", spaces.Spec{Name: "homelab", Members: []spaces.Member{
		{User: "sam", Role: spaces.RoleEditor},
	}})
	if _, err := f.sc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, ownerAuth := f.accountLogin(t, "owner", "owner-password")
	_, samAuth := f.accountLogin(t, "sam", "sam-password")

	status, body := doPost(t, f.ts, "POST", "/api/spaces/homelab/conventions", ownerAuth, nil)
	if status != http.StatusCreated {
		t.Fatalf("generate failed: %d %v", status, body)
	}
	raw, err := os.ReadFile(filepath.Join(f.dir, "homelab", "CONVENTIONS.md"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	for _, want := range []string{"Do not write an `id`", "[[Note title]]", "_assets", "# Conventions for homelab"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("conventions missing %q:\n%s", want, raw)
		}
	}

	// Without refresh, the existing file is returned untouched.
	status, body = doPost(t, f.ts, "POST", "/api/spaces/homelab/conventions", ownerAuth, nil)
	if status != http.StatusOK {
		t.Fatalf("second call failed: %d", status)
	}
	if created, _ := body["created"].(bool); created {
		t.Fatal("second call rewrote the file")
	}

	// A member editor may generate; a non-member space answers 404.
	status, _ = doPost(t, f.ts, "POST", "/api/spaces/homelab/conventions", samAuth, nil)
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("editor generate failed: %d", status)
	}
	status, _ = doPost(t, f.ts, "POST", "/api/spaces/elsewhere/conventions", samAuth, nil)
	if status != http.StatusNotFound {
		t.Fatalf("non-member space generate: %d", status)
	}

	// Refresh picks up new folders.
	if err := os.MkdirAll(filepath.Join(f.dir, "homelab", "rack"), 0o755); err != nil {
		t.Fatal(err)
	}
	status, body = doPost(t, f.ts, "POST", "/api/spaces/homelab/conventions", ownerAuth, map[string]any{"refresh": true})
	if status != http.StatusCreated {
		t.Fatalf("refresh failed: %d %v", status, body)
	}
	content, _ := body["content"].(string)
	if !strings.Contains(content, "rack/") {
		t.Fatalf("refresh missed the new folder:\n%s", content)
	}

	// The generated file becomes a note.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if n, err := f.db.GetNoteByPath(context.Background(), "homelab/CONVENTIONS.md"); err == nil && n.ID != "" {
			break
		} else if time.Now().After(deadline) {
			t.Fatal("conventions file was never indexed as a note")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
