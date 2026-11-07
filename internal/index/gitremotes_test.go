package index

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

// Migration 015 drops the HTTPS remotes (their tokens cannot become keys)
// and keeps SSH and path remotes, without a key yet.
func TestMigration015DropsHTTPSRemotes(t *testing.T) {
	p, raw := openAt(t, 14)
	for i, u := range []string{"https://github.com/me/n.git", "git@h:me/n.git", "/mnt/b.git"} {
		if _, err := raw.Exec(`INSERT INTO git_remotes (id, name, url, schedule, username, secret, created_at) VALUES (?, ?, ?, 'hourly', 'me', x'0102', ?)`,
			string(rune('a'+i)), string(rune('a'+i)), u, i+1); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()

	db, err := Open(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.ListGitRemotes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].URL != "git@h:me/n.git" || got[1].URL != "/mnt/b.git" {
		t.Fatalf("remotes after 015: %+v", got)
	}
	for _, r := range got {
		if r.PublicKey != "" || len(r.Secret) != 0 || r.Schedule != "hourly" {
			t.Fatalf("remote %s: %+v", r.ID, r)
		}
	}
}

func TestSetGitRemoteKey(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	r := GitRemote{ID: "r1", Name: "n", URL: "git@h:me/n.git", Schedule: "hourly", Enabled: true, CreatedAt: time.Now()}
	if err := db.CreateGitRemote(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := db.SetGitRemoteKey(ctx, "nope", "k", []byte("s")); !errors.Is(err, ErrGitRemoteNotFound) {
		t.Fatalf("missing id: %v", err)
	}
	if err := db.SetGitRemoteKey(ctx, "r1", "ssh-ed25519 AAAA", []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	r.Name, r.Schedule = "renamed", "nightly"
	if err := db.UpdateGitRemote(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetGitRemote(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicKey != "ssh-ed25519 AAAA" || !bytes.Equal(got.Secret, []byte("sealed")) || got.Name != "renamed" {
		t.Fatalf("after update: %+v", got)
	}
}
