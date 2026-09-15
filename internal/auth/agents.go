// Agent tokens: long-lived credentials for the MCP endpoint, distinct
// from user sessions. A token is scoped to named spaces, individually
// revocable, and every verification is logged. Only the SHA-256 of the
// secret is stored, like refresh tokens.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/spaces"
)

// Sentinel errors for the agent token surface.
var (
	// ErrBadLabel is a malformed agent label.
	ErrBadLabel = errors.New("label must be 1-64 characters: no colons, angle brackets, or control characters")
	// ErrBadScope is a malformed space in a token's scope.
	ErrBadScope = errors.New("spaces must be single directory names")
)

// AgentIdentity is the verified content of an agent token: which agent
// this is and what it may touch.
type AgentIdentity struct {
	TokenID  string
	Label    string
	Spaces   []string
	CanWrite bool
}

// Author is the CRDT/git author string for this agent.
func (a AgentIdentity) Author() string { return "agent:" + a.Label }

// ValidAgentLabel reports whether label is usable as an author label.
// The constraints come from the strings the rest of the system builds
// from it: "agent:<label>" must survive the rate limiter's prefix check
// and git's author parsing.
func ValidAgentLabel(label string) bool {
	if label == "" || len(label) > 64 || label == "filesystem" {
		return false
	}
	if strings.ContainsAny(label, ":<>\n\r") {
		return false
	}
	for _, r := range label {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// AgentToken is what CreateAgentToken hands back: the row and the one
// display of the secret.
type AgentToken struct {
	index.AgentToken
	Secret string `json:"secret,omitempty"`
}

// CreateAgentToken mints a token for an agent. Owner only. The secret is
// returned once; only its hash is kept.
func (s *Service) CreateAgentToken(ctx context.Context, id Identity, label string, spaceList []string, canWrite bool) (AgentToken, error) {
	if !id.Owner {
		return AgentToken{}, ErrNotOwner
	}
	label = strings.TrimSpace(label)
	if !ValidAgentLabel(label) {
		return AgentToken{}, ErrBadLabel
	}
	clean := make([]string, 0, len(spaceList))
	seen := map[string]bool{}
	for _, sp := range spaceList {
		sp = strings.TrimSpace(sp)
		if sp == "" {
			continue
		}
		if !spaces.ValidName(sp) {
			return AgentToken{}, fmt.Errorf("%w: %q", ErrBadScope, sp)
		}
		if !seen[sp] {
			seen[sp] = true
			clean = append(clean, sp)
		}
	}
	sort.Strings(clean)
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return AgentToken{}, err
	}
	secret := "ya_" + base64.RawURLEncoding.EncodeToString(b)
	now := s.opts.Now().UTC()
	row := index.AgentToken{
		ID: scanner.NewID(now), Label: label, Spaces: clean, CanWrite: canWrite,
		CreatedAt: now, LastUsedAt: now,
	}
	if err := s.db.CreateAgentToken(ctx, row, index.HashAgentToken(secret)); err != nil {
		return AgentToken{}, err
	}
	s.log.Info("agent token created", "id", row.ID, "label", label, "spaces", clean, "can_write", canWrite)
	return AgentToken{AgentToken: row, Secret: secret}, nil
}

// ListAgentTokens returns every token (owner only), secrets excluded.
func (s *Service) ListAgentTokens(ctx context.Context, id Identity) ([]index.AgentToken, error) {
	if !id.Owner {
		return nil, ErrNotOwner
	}
	return s.db.ListAgentTokens(ctx)
}

// RevokeAgentToken revokes one token (owner only). Revocation is
// immediate: the next MCP request with it fails.
func (s *Service) RevokeAgentToken(ctx context.Context, id Identity, tokenID string) error {
	if !id.Owner {
		return ErrNotOwner
	}
	if err := s.db.RevokeAgentToken(ctx, tokenID); err != nil {
		return err
	}
	s.log.Info("agent token revoked", "id", tokenID)
	return nil
}

// lastAgentUse throttles the last_used_at write to one a minute per
// token; a request every second must not produce a database write each
// time.
const lastAgentUse = time.Minute

// VerifyAgentToken resolves a secret to its agent. Unknown, malformed,
// and revoked secrets are indistinguishable. A use is logged and
// last_used_at touched at most once a minute.
func (s *Service) VerifyAgentToken(ctx context.Context, secret string) (AgentIdentity, error) {
	if secret == "" || len(secret) > 128 {
		return AgentIdentity{}, ErrInvalidToken
	}
	t, err := s.db.AgentTokenByHash(ctx, index.HashAgentToken(secret))
	if err != nil {
		return AgentIdentity{}, ErrInvalidToken
	}
	now := s.opts.Now()
	s.agentMu.Lock()
	if s.agentTouches == nil {
		s.agentTouches = map[string]time.Time{}
	}
	last := s.agentTouches[t.ID]
	if now.Sub(last) >= lastAgentUse {
		s.agentTouches[t.ID] = now
	}
	touch := now.Sub(last) >= lastAgentUse
	s.agentMu.Unlock()
	if touch {
		s.db.TouchAgentToken(ctx, t.ID)
		s.log.Info("agent token used", "id", t.ID, "label", t.Label)
	}
	return AgentIdentity{TokenID: t.ID, Label: t.Label, Spaces: t.Spaces, CanWrite: t.CanWrite}, nil
}
