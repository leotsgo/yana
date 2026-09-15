package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
)

func TestAgentTokenLifecycle(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	owner := Identity{UserID: "u1", Username: "owner", Owner: true}
	editor := Identity{UserID: "u2", Username: "sam"}

	if _, err := s.CreateAgentToken(ctx, editor, "docs", []string{"homelab"}, true); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("non-owner minted a token: %v", err)
	}
	tok, err := s.CreateAgentToken(ctx, owner, "docs", []string{"homelab", " homelab ", "garage"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Secret == "" || tok.ID == "" {
		t.Fatal("creation did not return a secret and id")
	}
	if len(tok.Spaces) != 2 || tok.Spaces[0] != "garage" || tok.Spaces[1] != "homelab" {
		t.Fatalf("scope not deduplicated and sorted: %v", tok.Spaces)
	}

	list, err := s.ListAgentTokens(ctx, owner)
	if err != nil || len(list) != 1 {
		t.Fatalf("listing: %v %d", err, len(list))
	}
	if _, err := s.ListAgentTokens(ctx, editor); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("non-owner listed tokens: %v", err)
	}

	// The label is taken until the token is revoked.
	if _, err := s.CreateAgentToken(ctx, owner, "docs", nil, false); !errors.Is(err, index.ErrAgentLabelTaken) {
		t.Fatalf("duplicate label accepted: %v", err)
	}

	agent, err := s.VerifyAgentToken(ctx, tok.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Label != "docs" || !agent.CanWrite || agent.Author() != "agent:docs" {
		t.Fatalf("verified identity is wrong: %+v", agent)
	}
	if agent.TokenID != tok.ID {
		t.Fatal("token id mismatch")
	}

	// A revoked token stops verifying; an unknown secret is the same
	// error.
	if err := s.RevokeAgentToken(ctx, owner, tok.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeAgentToken(ctx, editor, tok.ID); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("non-owner revoked a token: %v", err)
	}
	if _, err := s.VerifyAgentToken(ctx, tok.Secret); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revoked token still verifies: %v", err)
	}
	if _, err := s.VerifyAgentToken(ctx, "ya_nope"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown token error: %v", err)
	}

	// The label is free again after revocation.
	if _, err := s.CreateAgentToken(ctx, owner, "docs", nil, false); err != nil {
		t.Fatalf("label still taken after revocation: %v", err)
	}

	if err := s.RevokeAgentToken(ctx, owner, "missing"); !errors.Is(err, index.ErrAgentTokenNotFound) {
		t.Fatalf("revoking an unknown token: %v", err)
	}
}

func TestAgentTokenValidation(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	owner := Identity{UserID: "u1", Owner: true}

	bad := []string{"", "has:colon", "with <brackets>", "line\nbreak", "filesystem", "padding\x00"}
	for _, label := range bad {
		if _, err := s.CreateAgentToken(ctx, owner, label, nil, true); !errors.Is(err, ErrBadLabel) {
			t.Errorf("label %q accepted: %v", label, err)
		}
	}
	if _, err := s.CreateAgentToken(ctx, owner, "ok", []string{"../escape"}, true); !errors.Is(err, ErrBadScope) {
		t.Fatalf("bad space accepted: %v", err)
	}
	if !ValidAgentLabel("h") || !ValidAgentLabel("a-64-byte-label-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatal("reasonable labels rejected")
	}
	if ValidAgentLabel("") || ValidAgentLabel("agent:x") || ValidAgentLabel("filesystem") {
		t.Fatal("unreasonable labels accepted")
	}
}

func TestAgentTokenUseIsRecorded(t *testing.T) {
	s, db := newService(t)
	ctx := context.Background()
	owner := Identity{UserID: "u1", Owner: true}
	tok, err := s.CreateAgentToken(ctx, owner, "watcher", []string{"s"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.VerifyAgentToken(ctx, tok.Secret); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.ListAgentTokens(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("listing: %v %d", err, len(rows))
	}
	if rows[0].LastUsedAt.IsZero() || rows[0].LastUsedAt.Before(time.Now().Add(-time.Hour)) {
		t.Fatalf("use not recorded: %v", rows[0].LastUsedAt)
	}
	if rows[0].CanWrite {
		t.Fatal("read-only token reports write access")
	}
}
