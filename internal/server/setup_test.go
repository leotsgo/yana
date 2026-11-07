package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/leotsgo/yana/internal/guide"
)

// setupOwner runs the first-run setup and returns the owner's
// Authorization header.
func setupOwner(t *testing.T, f *authFixture) string {
	t.Helper()
	code, body := doPost(t, f.ts, "POST", "/api/auth/setup", "", map[string]string{"username": "owner", "password": "owner-password"})
	if code != http.StatusCreated {
		t.Fatalf("setup: %d %v", code, body)
	}
	return "Bearer " + body["tokens"].(map[string]any)["access_token"].(string)
}

// waitRestore polls the restore status until it reaches a final phase.
func waitRestore(t *testing.T, f *authFixture, auth string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		code, st := doGet(t, f.ts, "/api/setup/restore", auth)
		if code != http.StatusOK {
			t.Fatalf("restore status: %d %v", code, st)
		}
		if st["phase"] == "done" || st["phase"] == "failed" {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the restore did not finish within 10s")
	return nil
}

func TestSetupNoLongerSeedsGuide(t *testing.T) {
	f := newAuthGitFixture(t)
	owner := setupOwner(t, f)
	if _, err := os.Stat(filepath.Join(f.dir, guide.NoteName)); err == nil {
		t.Fatal("setup seeded the guide before the owner chose")
	}
	code, body := doGet(t, f.ts, "/api/setup", owner)
	if code != http.StatusOK || body["empty"] != true || body["restore"] != nil {
		t.Fatalf("setup state: %d %v", code, body)
	}
	if code, _ := doGet(t, f.ts, "/api/setup/restore", owner); code != http.StatusNotFound {
		t.Fatalf("restore status before any restore: %d", code)
	}
	code, body = doPost(t, f.ts, "POST", "/api/setup/empty", owner, map[string]any{})
	if code != http.StatusOK || body["path"] != guide.NoteName {
		t.Fatalf("empty: %d %v", code, body)
	}
	if _, err := f.db.GetNoteByPath(context.Background(), guide.NoteName); err != nil {
		t.Fatalf("starter note not indexed: %v", err)
	}
	if code, body := doGet(t, f.ts, "/api/setup", owner); code != http.StatusOK || body["empty"] != false {
		t.Fatalf("setup state after empty: %d %v", code, body)
	}
	code, body = doPost(t, f.ts, "POST", "/api/setup/empty", owner, map[string]any{})
	if code != http.StatusConflict || body["error"] != "The notes folder already holds notes." {
		t.Fatalf("second empty: %d %v", code, body)
	}
}

// seededBare is a bare repository holding one note, a/one.md.
func seededBare(t *testing.T) string {
	t.Helper()
	bare := bareRepo(t)
	work := t.TempDir()
	writeFile(t, work, "a/one.md", "---\nid: one-1\n---\n# One\n")
	gitOut(t, work, "init", "-q", "-b", "main")
	gitOut(t, work, "add", "-A")
	gitOut(t, work, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "one")
	gitOut(t, work, "push", "-q", bare, "main")
	return bare
}

func TestSetupRestoreFromPathRemote(t *testing.T) {
	f := newAuthGitFixture(t)
	owner := setupOwner(t, f)
	bare := seededBare(t)

	code, st := doPost(t, f.ts, "POST", "/api/setup/restore", owner, map[string]any{"url": bare})
	if code != http.StatusAccepted || st["url"] != bare {
		t.Fatalf("restore: %d %v", code, st)
	}
	st = waitRestore(t, f, owner)
	if st["phase"] != "done" || st["commits"] != float64(1) || st["notes"] != float64(1) || st["error"] != "" {
		t.Fatalf("final status: %v", st)
	}
	if _, err := f.db.GetNoteByPath(context.Background(), "a/one.md"); err != nil {
		t.Fatalf("a/one.md not indexed: %v", err)
	}
	remotes, err := f.db.ListGitRemotes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 1 || remotes[0].Name != "backup" || remotes[0].Schedule != "nightly" || remotes[0].PushHour != 2 ||
		!remotes[0].Enabled || remotes[0].URL != bare || len(remotes[0].Secret) != 0 {
		t.Fatalf("remotes: %+v", remotes)
	}
	if code, body := doGet(t, f.ts, "/api/setup", owner); code != http.StatusOK || body["empty"] != false {
		t.Fatalf("setup state after restore: %d %v", code, body)
	}
	if code, body := doPost(t, f.ts, "POST", "/api/setup/restore", owner, map[string]any{"url": bare}); code != http.StatusConflict ||
		body["error"] != "The notes folder already holds notes." {
		t.Fatalf("restore over notes: %d %v", code, body)
	}
}

func TestSetupRestoreRefusals(t *testing.T) {
	f := newAuthGitFixture(t)
	owner := setupOwner(t, f)
	if code, _ := doPost(t, f.ts, "POST", "/api/setup/restore", owner, map[string]any{"url": "not a url"}); code != http.StatusBadRequest {
		t.Fatalf("bad url: %d", code)
	}
	if code, _ := doPost(t, f.ts, "POST", "/api/setup/restore", owner, map[string]any{"url": "git@example.invalid:me/notes.git"}); code != http.StatusBadRequest {
		t.Fatalf("ssh without a key: %d", code)
	}
	if code, _ := doPost(t, f.ts, "POST", "/api/setup/restore", owner, map[string]any{"url": "git@example.invalid:me/notes.git", "sealed_key": "forged"}); code != http.StatusBadRequest {
		t.Fatalf("foreign key: %d", code)
	}
	if code, _ := doPost(t, f.ts, "POST", "/api/setup/restore", owner, map[string]any{"url": bareRepo(t), "sealed_key": "x"}); code != http.StatusBadRequest {
		t.Fatalf("path with a key: %d", code)
	}
	_, member := f.account(t, "member", "member-password")
	for _, ep := range []string{"/api/setup", "/api/setup/restore"} {
		if code, _ := doGet(t, f.ts, ep, member); code != http.StatusForbidden {
			t.Fatalf("GET %s as a member: %d", ep, code)
		}
	}
	if code, _ := doPost(t, f.ts, "POST", "/api/setup/empty", member, map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("empty as a member: %d", code)
	}
}

func TestSetupRestoreOneAtATime(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\nsleep 2\nprintf 0000\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Fatal(err)
	}
	f := newAuthGitFixture(t)
	owner := setupOwner(t, f)
	code, key := doPost(t, f.ts, "POST", "/api/git/keys", owner, map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("keys: %d %v", code, key)
	}
	body := map[string]any{"url": "git@example.invalid:me/notes.git", "sealed_key": key["sealed_key"]}
	if code, st := doPost(t, f.ts, "POST", "/api/setup/restore", owner, body); code != http.StatusAccepted {
		t.Fatalf("first restore: %d %v", code, st)
	}
	code, res := doPost(t, f.ts, "POST", "/api/setup/restore", owner, body)
	if code != http.StatusConflict || res["error"] != "A restore is already running." {
		t.Fatalf("second restore: %d %v", code, res)
	}
	if code, res := doPost(t, f.ts, "POST", "/api/setup/empty", owner, map[string]any{}); code != http.StatusConflict {
		t.Fatalf("empty during a restore: %d %v", code, res)
	}
	// The fake remote has no branch: the restore ends as an empty backup.
	if st := waitRestore(t, f, owner); st["phase"] != "failed" || st["error"] != "That repository is empty; there is nothing to restore." {
		t.Fatalf("final status: %v", st)
	}
}

func TestSetupRestoreFailureIsExplained(t *testing.T) {
	f := newAuthGitFixture(t)
	owner := setupOwner(t, f)
	if code, st := doPost(t, f.ts, "POST", "/api/setup/restore", owner, map[string]any{"url": "/nonexistent"}); code != http.StatusAccepted {
		t.Fatalf("restore: %d %v", code, st)
	}
	st := waitRestore(t, f, owner)
	if st["phase"] != "failed" || st["error"] == "" {
		t.Fatalf("final status: %v", st)
	}
	code, body := doGet(t, f.ts, "/api/setup", owner)
	if code != http.StatusOK || body["empty"] != true {
		t.Fatalf("setup state after a failed restore: %d %v", code, body)
	}
	if r, _ := body["restore"].(map[string]any); r["phase"] != "failed" {
		t.Fatalf("setup state carries the failed restore: %v", body)
	}
	if remotes, err := f.db.ListGitRemotes(context.Background()); err != nil || len(remotes) != 0 {
		t.Fatalf("remotes after a failed restore: %v %+v", err, remotes)
	}
}
