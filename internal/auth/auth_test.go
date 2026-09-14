package auth

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/spaces"
)

func newService(t *testing.T) (*Service, *index.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := index.Open(filepath.Join(dir, "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := Open(db, filepath.Join(dir, "secret"), Options{
		AccessTTL:  50 * time.Millisecond,
		RefreshTTL: time.Hour,
		Now:        func() time.Time { return time.Now().UTC() },
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

func TestHashAndVerifyPassword(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("hash is not PHC argon2id: %s", h)
	}
	if !VerifyPassword(h, "correct horse battery staple") {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword(h, "wrong password") {
		t.Fatal("wrong password accepted")
	}
	if VerifyPassword("garbage", "x") {
		t.Fatal("malformed hash accepted")
	}
	// Two hashes of one password differ (random salt).
	h2, _ := HashPassword("correct horse battery staple")
	if h == h2 {
		t.Fatal("salt missing: identical hashes")
	}
}

func TestSetupLoginRefreshLogout(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()

	if s.HasAccounts(ctx) {
		t.Fatal("fresh service reports accounts")
	}
	if _, _, err := s.Login(ctx, "ada", "password123", "test"); err == nil {
		t.Fatal("login worked before setup")
	}
	u, tok, err := s.Setup(ctx, "Ada", "password123", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if !u.IsOwner || u.Username != "ada" {
		t.Fatalf("setup user: %+v", u)
	}
	if _, _, err := s.Setup(ctx, "eve", "password123", "laptop"); err == nil {
		t.Fatal("second setup allowed")
	}

	id, err := s.VerifyAccess(tok.Access)
	if err != nil || id.Username != "ada" || !id.Owner || id.SessionID != tok.SessionID {
		t.Fatalf("access token verify: %+v %v", id, err)
	}

	if _, _, err := s.Login(ctx, "ada", "wrong-pass", "x"); err == nil {
		t.Fatal("wrong password accepted")
	}
	_, tok2, err := s.Login(ctx, "ADA", "password123", "phone")
	if err != nil {
		t.Fatal(err)
	}

	refreshed, err := s.Refresh(ctx, tok2.Refresh)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := s.VerifyAccess(refreshed.Access); err != nil || id.SessionID != tok2.SessionID {
		t.Fatalf("refreshed token: %+v %v", id, err)
	}
	if _, err := s.Refresh(ctx, "yr_bogus"); err == nil {
		t.Fatal("unknown refresh accepted")
	}

	sessions, err := s.Sessions(ctx, Identity{UserID: u.ID})
	if err != nil || len(sessions) != 2 {
		t.Fatalf("sessions: %v %v", sessions, err)
	}

	if err := s.Logout(ctx, tok2.Refresh); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refresh(ctx, tok2.Refresh); err == nil {
		t.Fatal("revoked session refreshed")
	}
}

func TestAccessTokenExpiry(t *testing.T) {
	s, _ := newService(t)
	_, tok, err := s.Setup(context.Background(), "ada", "password123", "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyAccess(tok.Access); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := s.VerifyAccess(tok.Access); err == nil {
		t.Fatal("expired token accepted")
	}
	// Tampering with the body breaks the signature.
	_, tok2, _ := s.Login(context.Background(), "ada", "password123", "x")
	parts := strings.Split(tok2.Access, ".")
	forged := parts[0] + "." + strings.Repeat("A", len(parts[1])) + "." + parts[2]
	if _, err := s.VerifyAccess(forged); err == nil {
		t.Fatal("forged token accepted")
	}
}

func TestUserManagement(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	_, tok, err := s.Setup(ctx, "ada", "password123", "x")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.VerifyAccess(tok.Access)
	if err != nil {
		t.Fatal(err)
	}

	sam, err := s.CreateUser(ctx, id, "sam", "password456")
	if err != nil {
		t.Fatal(err)
	}
	if sam.IsOwner {
		t.Fatal("created user must not be a global owner")
	}
	if _, err := s.CreateUser(ctx, id, "SAM", "password456"); err == nil {
		t.Fatal("case-insensitive duplicate accepted")
	}
	if _, err := s.CreateUser(ctx, id, "x", "password456"); err == nil {
		t.Fatal("short username accepted")
	}
	if _, err := s.CreateUser(ctx, id, "xavier", "short"); err == nil {
		t.Fatal("short password accepted")
	}

	// A non-owner cannot manage users.
	_, samTok, err := s.Login(ctx, "sam", "password456", "a")
	if err != nil {
		t.Fatal(err)
	}
	samID, err := s.VerifyAccess(samTok.Access)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Users(ctx, samID); err == nil {
		t.Fatal("non-owner listed users")
	}
	if err := s.DeleteUser(ctx, samID, sam.ID); err == nil {
		t.Fatal("non-owner deleted a user")
	}

	users, err := s.Users(ctx, id)
	if err != nil || len(users) != 2 {
		t.Fatalf("users: %v %v", users, err)
	}

	// Password change revokes other sessions but keeps the caller's.
	_, samTok2, _ := s.Login(ctx, "sam", "password456", "b")
	samIdent, err := s.VerifyAccess(samTok2.Access)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword(ctx, samIdent, sam.ID, "password789"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refresh(ctx, samTok.Refresh); err == nil {
		t.Fatal("other session survived the password change")
	}
	if _, err := s.Refresh(ctx, samTok2.Refresh); err != nil {
		t.Fatalf("caller's session was revoked by their own password change: %v", err)
	}

	if err := s.DeleteUser(ctx, id, sam.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, id, id.UserID); err == nil {
		t.Fatal("owner deleted their own account")
	}
}

func TestSessionRevokedCallback(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	_, tok, err := s.Setup(ctx, "ada", "password123", "x")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	done := make(chan struct{})
	s.OnSessionRevoked(func(sid string) {
		got = append(got, sid)
		if len(got) == 1 {
			close(done)
		}
	})
	id, err := s.VerifyAccess(tok.Access)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeSession(ctx, id, tok.SessionID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("revocation callback never fired")
	}
	if got[0] != tok.SessionID {
		t.Fatalf("callback session: %v", got)
	}
}

func TestAuthorizeNote(t *testing.T) {
	s, db := newService(t)
	ctx := context.Background()
	owner, _, err := s.Setup(ctx, "ada", "password123", "x")
	if err != nil {
		t.Fatal(err)
	}
	sam, err := s.CreateUser(ctx, Identity{UserID: owner.ID, Username: "ada", Owner: true}, "sam", "password456")
	if err != nil {
		t.Fatal(err)
	}
	eve, err := s.CreateUser(ctx, Identity{UserID: owner.ID, Username: "ada", Owner: true}, "eve", "password456")
	if err != nil {
		t.Fatal(err)
	}

	note := index.Note{ID: scanner.NewID(time.Now()), Space: "home", RelPath: "home/x.md", Title: "x", Kind: "md", ContentHash: "h", Created: time.Now(), UpdatedAt: time.Now(), MTime: time.Now()}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		return index.UpsertNote(tx, note, "body", nil)
	}); err != nil {
		t.Fatal(err)
	}
	spec := spaces.Spec{Name: "home", Members: []spaces.Member{
		{User: sam.ID, Role: spaces.RoleViewer},
	}}
	if _, err := db.SyncSpaceSpec(ctx, "home", spec, nil); err != nil {
		t.Fatal(err)
	}

	ownerID := Identity{UserID: owner.ID, Username: "ada", Owner: true}
	if _, role, err := s.AuthorizeNote(ctx, ownerID, note.ID); err != nil || role != "owner" {
		t.Fatalf("global owner: %v %v", role, err)
	}
	samID := Identity{UserID: sam.ID, Username: "sam"}
	if _, role, err := s.AuthorizeNote(ctx, samID, note.ID); err != nil || role != "viewer" {
		t.Fatalf("member: %v %v", role, err)
	}
	eveID := Identity{UserID: eve.ID, Username: "eve"}
	if _, _, err := s.AuthorizeNote(ctx, eveID, note.ID); err != ErrForbidden {
		t.Fatalf("non-member: %v", err)
	}
	// The subscribe adapter path: author strings.
	if role, err := s.AuthorizeSubscribe(ctx, "user:sam", note.ID); err != nil || role != "viewer" {
		t.Fatalf("subscribe viewer: %v %v", role, err)
	}
	if _, err := s.AuthorizeSubscribe(ctx, "user:eve", note.ID); err != ErrForbidden {
		t.Fatalf("subscribe non-member: %v", err)
	}
	if _, err := s.AuthorizeSubscribe(ctx, "agent:robot", note.ID); err != ErrForbidden {
		t.Fatalf("agent author must be refused under account auth: %v", err)
	}
	// Write checks.
	if err := s.CanWriteSpace(ctx, samID, "home"); err != ErrReadOnly {
		t.Fatalf("viewer write: %v", err)
	}
	if err := s.CanWriteSpace(ctx, eveID, "home"); err != ErrForbidden {
		t.Fatalf("non-member write: %v", err)
	}
}
