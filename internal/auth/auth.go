// Package auth holds accounts and sessions: Argon2id password hashing,
// short-lived signed access tokens, long-lived refresh tokens stored
// only as hashes, device-labelled sessions with revocation, and the
// membership lookups the REST middleware and the realtime relay share.
//
// There are no default credentials. The first account is created
// through the first-run setup flow and becomes the global owner, who is
// implicitly a member of every space.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/spaces"
)

// Options tune one service.
type Options struct {
	// AccessTTL is how long an access token lives (15 minutes).
	AccessTTL time.Duration
	// RefreshTTL is how long a session may go unused (30 days).
	RefreshTTL time.Duration
	// Now is the clock.
	Now func() time.Time
}

func (o *Options) defaults() {
	if o.AccessTTL <= 0 {
		o.AccessTTL = 15 * time.Minute
	}
	if o.RefreshTTL <= 0 {
		o.RefreshTTL = 30 * 24 * time.Hour
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNoAccounts reports that setup has not run yet.
	ErrNoAccounts = errors.New("no account exists yet; run first-run setup")
	// ErrBadCredentials is a wrong username or password.
	ErrBadCredentials = errors.New("wrong username or password")
	// ErrInvalidToken is an expired, malformed, or forged token.
	ErrInvalidToken = errors.New("access token is invalid or expired")
	// ErrSessionRejected is an unknown, expired, or revoked refresh token.
	ErrSessionRejected = errors.New("session is unknown, expired, or revoked")
	// ErrWeakPassword is a password under the minimum length.
	ErrWeakPassword = errors.New("password must be at least 8 characters")
	// ErrBadUsername is a malformed username.
	ErrBadUsername = errors.New("username must be 2-32 characters: letters, digits, dot, underscore, dash")
	// ErrNotOwner is an action reserved for the global owner.
	ErrNotOwner = errors.New("this action needs the owner account")
	// ErrForbidden is missing membership or an insufficient role.
	ErrForbidden = errors.New("you are not a member of this space")
	// ErrReadOnly is a write attempted with the viewer role.
	ErrReadOnly = errors.New("this space is read-only for your account")
	// ErrTooManyAttempts is login throttling.
	ErrTooManyAttempts = errors.New("too many attempts; wait a minute and try again")
)

const minPasswordLen = 8

// Service manages accounts, sessions, and authorization.
type Service struct {
	db     *index.DB
	secret []byte
	log    *slog.Logger
	opts   Options

	loginLimit *pathsafe.RateLimiter

	// agentMu guards agentTouches, the last_used_at write throttle for
	// agent tokens.
	agentMu      sync.Mutex
	agentTouches map[string]time.Time

	mu      sync.Mutex
	revokeC []func(sessionID string)
	// revoked holds sessions revoked while this process runs, each until
	// the last access token minted for it has expired, so a revocation
	// takes effect on the next request rather than at token expiry and
	// verification still reads no database.
	revoked map[string]time.Time
}

// Open builds the service. The signing secret is created on first use
// and kept next to the index (path, 0600); losing it invalidates every
// access token, which costs everyone a refresh and nothing more.
func Open(db *index.DB, secretPath string, opts Options, log *slog.Logger) (*Service, error) {
	opts.defaults()
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o700); err != nil {
		return nil, fmt.Errorf("auth: create secret dir: %w", err)
	}
	secret, err := loadOrCreateSecret(secretPath)
	if err != nil {
		return nil, err
	}
	return &Service{
		db:         db,
		secret:     secret,
		log:        log.With("component", "auth"),
		opts:       opts,
		loginLimit: pathsafe.NewRateLimiter(pathsafe.Rate{N: 10, Window: time.Minute}, pathsafe.Rate{N: 10, Window: time.Minute}),
	}, nil
}

func loadOrCreateSecret(path string) ([]byte, error) {
	b, err := fsutil.LoadOrCreateSecret(path)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return b, nil
}

// OnSessionRevoked registers a callback fired with each revoked session
// id, so live connections can be severed in the same request.
func (s *Service) OnSessionRevoked(fn func(sessionID string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revokeC = append(s.revokeC, fn)
}

func (s *Service) fireRevoked(ids ...string) {
	now := s.opts.Now()
	s.mu.Lock()
	if s.revoked == nil {
		s.revoked = map[string]time.Time{}
	}
	for id, until := range s.revoked {
		if !now.Before(until) {
			delete(s.revoked, id)
		}
	}
	for _, id := range ids {
		s.revoked[id] = now.Add(s.opts.AccessTTL)
	}
	cbs := make([]func(string), len(s.revokeC))
	copy(cbs, s.revokeC)
	s.mu.Unlock()
	for _, id := range ids {
		for _, cb := range cbs {
			cb(id)
		}
	}
}

// --- passwords ------------------------------------------------------------

// Argon2id parameters: 64 MiB, one pass, four lanes — about 50ms on a
// small server, well under a second on a phone.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword derives the PHC-format Argon2id string for storage.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the stored PHC hash.
func VerifyPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	// "", "argon2id", "v=19", "m=...,t=...,p=...", salt, key
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return hmac.Equal(got, want)
}

// --- access tokens ----------------------------------------------------------

// Identity is who a request is: the verified content of an access
// token.
type Identity struct {
	UserID    string
	Username  string
	Owner     bool
	SessionID string
	Expires   time.Time
}

// Author is the CRDT/git author string for this identity.
func (id Identity) Author() string { return "user:" + id.Username }

type accessClaims struct {
	Sub string `json:"sub"`
	Usr string `json:"usr"`
	Own bool   `json:"own"`
	Sid string `json:"sid"`
	Exp int64  `json:"exp"` // unix nanoseconds, so short token lives stay exact
}

// Tokens is one login's issued pair.
type Tokens struct {
	Access      string    `json:"access_token"`
	AccessExp   time.Time `json:"access_expires_at"`
	Refresh     string    `json:"refresh_token"`
	SessionID   string    `json:"session_id"`
	RefreshExpi time.Time `json:"refresh_expires_at"`
}

// newAccessToken signs the identity's claims with HMAC-SHA256.
func (s *Service) newAccessToken(u index.User, sessionID string) (string, time.Time, error) {
	exp := s.opts.Now().Add(s.opts.AccessTTL).UTC()
	claims, err := json.Marshal(accessClaims{Sub: u.ID, Usr: u.Username, Own: u.IsOwner, Sid: sessionID, Exp: exp.UnixNano()})
	if err != nil {
		return "", time.Time{}, err
	}
	body := "v1." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), exp, nil
}

// VerifyAccess checks a token's signature and expiry.
func (s *Service) VerifyAccess(token string) (Identity, error) {
	rest, ok := strings.CutPrefix(token, "v1.")
	if !ok {
		return Identity{}, ErrInvalidToken
	}
	body, sig, ok := strings.Cut(rest, ".")
	if !ok {
		return Identity{}, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte("v1." + body))
	want, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(mac.Sum(nil), want) {
		return Identity{}, ErrInvalidToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return Identity{}, ErrInvalidToken
	}
	var c accessClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return Identity{}, ErrInvalidToken
	}
	if !s.opts.Now().UTC().Before(time.Unix(0, c.Exp)) {
		return Identity{}, ErrInvalidToken
	}
	s.mu.Lock()
	_, gone := s.revoked[c.Sid]
	s.mu.Unlock()
	if gone {
		return Identity{}, ErrInvalidToken
	}
	return Identity{UserID: c.Sub, Username: c.Usr, Owner: c.Own, SessionID: c.Sid, Expires: time.Unix(0, c.Exp).UTC()}, nil
}

// --- sessions ---------------------------------------------------------------

func newRefreshToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = "yr_" + base64.RawURLEncoding.EncodeToString(b)
	return token, hashRefresh(token), nil
}

func hashRefresh(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// HasAccounts reports whether setup already ran.
func (s *Service) HasAccounts(ctx context.Context) bool {
	n, err := s.db.CountUsers(ctx)
	return err == nil && n > 0
}

// Setup creates the first owner account. It only works while no account
// exists; there are no default credentials to fall back to.
func (s *Service) Setup(ctx context.Context, username, password, label string) (index.User, Tokens, error) {
	if s.HasAccounts(ctx) {
		return index.User{}, Tokens{}, errors.New("setup already ran; sign in instead")
	}
	u, err := s.createUser(ctx, username, password, true)
	if err != nil {
		return index.User{}, Tokens{}, err
	}
	tok, err := s.openSession(ctx, u, label)
	return u, tok, err
}

// Login verifies a password and opens a session.
func (s *Service) Login(ctx context.Context, username, password, label string) (index.User, Tokens, error) {
	if !s.loginLimit.Allow("login:" + strings.ToLower(username)) {
		return index.User{}, Tokens{}, ErrTooManyAttempts
	}
	u, err := s.db.GetUserByName(ctx, username)
	if err != nil {
		// Burn a hash comparison anyway so timing does not reveal which
		// usernames exist.
		VerifyPassword("$argon2id$v=19$m=65536,t=1,p=4$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", password)
		return index.User{}, Tokens{}, ErrBadCredentials
	}
	if !VerifyPassword(u.PasswordHash, password) {
		return index.User{}, Tokens{}, ErrBadCredentials
	}
	tok, err := s.openSession(ctx, u, label)
	return u, tok, err
}

func (s *Service) openSession(ctx context.Context, u index.User, label string) (Tokens, error) {
	if label == "" {
		label = "session"
	}
	if len(label) > 100 {
		label = label[:100]
	}
	now := s.opts.Now().UTC()
	refresh, hash, err := newRefreshToken()
	if err != nil {
		return Tokens{}, err
	}
	sid := scanner.NewID(now)
	expires := now.Add(s.opts.RefreshTTL)
	if err := s.db.CreateSession(ctx, index.Session{
		ID: sid, UserID: u.ID, Label: label,
		CreatedAt: now, LastUsedAt: now, ExpiresAt: expires,
	}, hash); err != nil {
		return Tokens{}, err
	}
	access, accessExp, err := s.newAccessToken(u, sid)
	if err != nil {
		return Tokens{}, err
	}
	s.log.Info("session opened", "user", u.Username, "session", sid, "label", label)
	return Tokens{Access: access, AccessExp: accessExp, Refresh: refresh, SessionID: sid, RefreshExpi: expires}, nil
}

// Refresh trades a refresh token for a new access token on the same
// session.
func (s *Service) Refresh(ctx context.Context, refresh string) (Tokens, error) {
	if refresh == "" {
		return Tokens{}, ErrSessionRejected
	}
	sess, err := s.db.SessionByRefreshHash(ctx, hashRefresh(refresh), s.opts.Now())
	if err != nil {
		return Tokens{}, ErrSessionRejected
	}
	u, err := s.db.GetUser(ctx, sess.UserID)
	if err != nil {
		return Tokens{}, ErrSessionRejected
	}
	access, accessExp, err := s.newAccessToken(u, sess.ID)
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{Access: access, AccessExp: accessExp, Refresh: refresh, SessionID: sess.ID, RefreshExpi: sess.ExpiresAt}, nil
}

// Logout revokes the session a refresh token belongs to.
func (s *Service) Logout(ctx context.Context, refresh string) error {
	if refresh == "" {
		return ErrSessionRejected
	}
	sess, err := s.db.SessionByRefreshHash(ctx, hashRefresh(refresh), s.opts.Now())
	if err != nil {
		return ErrSessionRejected
	}
	if err := s.db.RevokeSession(ctx, sess.UserID, sess.ID, false); err != nil {
		return err
	}
	s.fireRevoked(sess.ID)
	s.log.Info("session revoked", "session", sess.ID)
	return nil
}

// Sessions lists the identity's sessions.
func (s *Service) Sessions(ctx context.Context, id Identity) ([]index.Session, error) {
	return s.db.ListSessions(ctx, id.UserID)
}

// RevokeSession revokes one session. Users revoke their own; the global
// owner may revoke anyone's.
func (s *Service) RevokeSession(ctx context.Context, id Identity, sessionID string) error {
	if err := s.db.RevokeSession(ctx, id.UserID, sessionID, id.Owner); err != nil {
		return err
	}
	s.fireRevoked(sessionID)
	return nil
}

// --- user management ----------------------------------------------------------

func (s *Service) createUser(ctx context.Context, username, password string, owner bool) (index.User, error) {
	username = strings.TrimSpace(strings.ToLower(username))
	if !index.ValidUsername(username) {
		return index.User{}, ErrBadUsername
	}
	if len(password) < minPasswordLen {
		return index.User{}, ErrWeakPassword
	}
	hash, err := HashPassword(password)
	if err != nil {
		return index.User{}, err
	}
	u := index.User{ID: scanner.NewID(s.opts.Now()), Username: username, PasswordHash: hash, IsOwner: owner, CreatedAt: s.opts.Now().UTC()}
	if err := s.db.CreateUser(ctx, u); err != nil {
		return index.User{}, err
	}
	s.log.Info("account created", "user", username, "owner", owner)
	return u, nil
}

// Users lists accounts (owner only).
func (s *Service) Users(ctx context.Context, id Identity) ([]index.User, error) {
	if !id.Owner {
		return nil, ErrNotOwner
	}
	return s.db.ListUsers(ctx)
}

// CreateUser adds an account (owner only).
func (s *Service) CreateUser(ctx context.Context, id Identity, username, password string) (index.User, error) {
	if !id.Owner {
		return index.User{}, ErrNotOwner
	}
	return s.createUser(ctx, username, password, false)
}

// DeleteUser removes an account, revoking its sessions (owner only, and
// not the owner's own account).
func (s *Service) DeleteUser(ctx context.Context, id Identity, userID string) error {
	if !id.Owner {
		return ErrNotOwner
	}
	if userID == id.UserID {
		return errors.New("the owner account cannot delete itself")
	}
	ids, err := s.db.RevokeUserSessions(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.db.DeleteUser(ctx, userID); err != nil {
		return err
	}
	s.fireRevoked(ids...)
	s.log.Info("account deleted", "id", userID)
	return nil
}

// SetPassword replaces a password and revokes the user's other
// sessions. A user may change their own; the owner may change anyone's.
func (s *Service) SetPassword(ctx context.Context, id Identity, userID, password string) error {
	if !id.Owner && userID != id.UserID {
		return ErrNotOwner
	}
	if len(password) < minPasswordLen {
		return ErrWeakPassword
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.db.SetPasswordHash(ctx, userID, hash); err != nil {
		return err
	}
	ids, err := s.db.RevokeUserSessions(ctx, userID, id.SessionID)
	if err != nil {
		return err
	}
	s.fireRevoked(ids...)
	return nil
}

// --- authorization ------------------------------------------------------------

// roleFor applies the global owner's implicit membership.
func roleFor(id Identity, role string, member bool) string {
	if id.Owner {
		return spaces.RoleOwner
	}
	if member {
		return role
	}
	return ""
}

// AuthorizeNote answers which space a note is in and the identity's
// role there, in one lookup. ErrForbidden means no membership;
// index.ErrNotFound means the note does not exist.
func (s *Service) AuthorizeNote(ctx context.Context, id Identity, noteID string) (space, role string, err error) {
	space, memberRole, member, err := s.db.NoteAuthz(ctx, id.UserID, noteID)
	if err != nil {
		return "", "", err
	}
	role = roleFor(id, memberRole, member)
	if role == "" {
		return space, "", ErrForbidden
	}
	return space, role, nil
}

// AuthorizeSpace answers the identity's role in one space.
func (s *Service) AuthorizeSpace(ctx context.Context, id Identity, space string) (string, error) {
	if id.Owner {
		return spaces.RoleOwner, nil
	}
	role, member, err := s.db.MemberRole(ctx, id.UserID, space)
	if err != nil {
		return "", err
	}
	if !member {
		return "", ErrForbidden
	}
	return role, nil
}

// MemberSpaces lists the spaces the identity may see. all is true for
// the global owner (every space, including ones with no .space.yml).
func (s *Service) MemberSpaces(ctx context.Context, id Identity) (list []string, all bool, err error) {
	if id.Owner {
		return nil, true, nil
	}
	list, err = s.db.MemberSpaces(ctx, id.UserID)
	return list, false, err
}

// CanWriteSpace reports whether the identity may change a space
// (editor or better).
func (s *Service) CanWriteSpace(ctx context.Context, id Identity, space string) error {
	role, err := s.AuthorizeSpace(ctx, id, space)
	if err != nil {
		return err
	}
	if !spaces.RoleAtLeast(role, spaces.RoleEditor) {
		return ErrReadOnly
	}
	return nil
}

// --- realtime relay adapter ----------------------------------------------------

// SubscribeAuthz implements the relay's authorizer: it resolves an
// author string ("user:<name>") and the note's space in a single query
// and returns the role the connection holds in the room.
func (s *Service) AuthorizeSubscribe(ctx context.Context, author, noteID string) (string, error) {
	u, _, role, err := index.UserForAuthor(ctx, s.db.Reader(), author, noteID)
	if err != nil {
		if errors.Is(err, index.ErrUserNotFound) {
			return "", ErrForbidden
		}
		return "", err
	}
	role = roleFor(Identity{UserID: u.ID, Username: u.Username, Owner: u.IsOwner}, role, role != "")
	if role == "" {
		return "", ErrForbidden
	}
	return role, nil
}

// NoteSpace tells the relay which space a note belongs to, so a
// membership change can re-check the right rooms.
func (s *Service) NoteSpace(ctx context.Context, noteID string) (string, error) {
	return s.db.NoteSpace(ctx, noteID)
}
