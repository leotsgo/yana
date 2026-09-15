// Accounts and sessions: the first-run setup flow, sign-in, token
// refresh, session listing and revocation, and the middleware that
// gates every other /api route on a verified identity. There are no
// default credentials; until setup runs, the API answers setup_required
// and nothing else.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/auth"
	"github.com/madeofpendletonwool/yana/internal/index"
)

const refreshTokenCookie = "yana_refresh"

// identityCtx carries the verified identity through the request.
type identityCtx struct{ id auth.Identity }

// ident returns the request's identity, or the zero value when the
// server runs without accounts (Deps.Auth nil).
func (s *Server) ident(r *http.Request) auth.Identity {
	if v, ok := r.Context().Value(identityCtx{}).(auth.Identity); ok {
		return v
	}
	return auth.Identity{}
}

// open reports whether the server runs without accounts.
func (s *Server) open() bool { return s.Auth == nil }

// bearerToken reads the access token from the Authorization header. Asset
// reads (GET /api/files/...) also accept it as ?token=, the way /ws does,
// because an <img> in a rendered note cannot set a header.
func bearerToken(r *http.Request) string {
	if t := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); t != "" {
		return t
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/files/") {
		return r.URL.Query().Get("token")
	}
	return ""
}

// authed wraps every /api route that is not one of the public auth
// endpoints. Without accounts configured it reports what the client
// must do next instead of a bare 401.
func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.open() {
			next(w, r)
			return
		}
		id, err := s.Auth.VerifyAccess(bearerToken(r))
		if err != nil {
			if !s.Auth.HasAccounts(r.Context()) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"error":          "no account exists yet; create the owner account first",
					"setup_required": true,
				})
				return
			}
			writeError(w, http.StatusUnauthorized, "sign in first (a valid access token is required)")
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), identityCtx{}, id))
		next(w, r)
	}
}

// --- first-run and sign-in ------------------------------------------------

func (s *Server) handleAuthState(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"setup_required": true}
	if s.open() || s.Auth.HasAccounts(r.Context()) {
		resp["setup_required"] = false
	}
	if !s.open() {
		if id, err := s.Auth.VerifyAccess(bearerToken(r)); err == nil {
			resp["user"] = map[string]any{"id": id.UserID, "username": id.Username, "is_owner": id.Owner}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleSetup creates the owner account. It only works once; after
// that it is an ordinary sign-in world.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if s.open() {
		writeError(w, http.StatusNotImplemented, "this server runs without accounts")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Label    string `json:"label"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	u, tok, err := s.Auth.Setup(r.Context(), body.Username, body.Password, body.Label)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	s.setRefreshCookie(w, tok.Refresh, tok.RefreshExpi)
	writeJSON(w, http.StatusCreated, map[string]any{
		"user":   map[string]any{"id": u.ID, "username": u.Username, "is_owner": u.IsOwner},
		"tokens": tok,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.open() {
		writeError(w, http.StatusNotImplemented, "this server runs without accounts")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Label    string `json:"label"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	if body.Label == "" {
		body.Label = deviceLabel(r.UserAgent())
	}
	u, tok, err := s.Auth.Login(r.Context(), body.Username, body.Password, body.Label)
	if err != nil {
		if errors.Is(err, auth.ErrTooManyAttempts) {
			writeError(w, http.StatusTooManyRequests, err.Error())
			return
		}
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	s.setRefreshCookie(w, tok.Refresh, tok.RefreshExpi)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":   map[string]any{"id": u.ID, "username": u.Username, "is_owner": u.IsOwner},
		"tokens": tok,
	})
}

// handleRefresh trades a refresh token (cookie or body) for a new
// access token.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if s.open() {
		writeError(w, http.StatusNotImplemented, "this server runs without accounts")
		return
	}
	var body struct {
		Refresh string `json:"refresh_token"`
	}
	// The body is optional: the HttpOnly cookie is the usual carrier.
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body)
	refresh := body.Refresh
	if refresh == "" {
		if c, err := r.Cookie(refreshTokenCookie); err == nil {
			refresh = c.Value
		}
	}
	tok, err := s.Auth.Refresh(r.Context(), refresh)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "sign in again")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tok})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.open() {
		writeError(w, http.StatusNotImplemented, "this server runs without accounts")
		return
	}
	var body struct {
		Refresh string `json:"refresh_token"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body)
	refresh := body.Refresh
	if refresh == "" {
		if c, err := r.Cookie(refreshTokenCookie); err == nil {
			refresh = c.Value
		}
	}
	if err := s.Auth.Logout(r.Context(), refresh); err != nil {
		// Logging out with an unknown token is a success from the
		// client's point of view; the cookie goes either way.
		s.Log.Debug("logout with unknown session")
	}
	s.clearRefreshCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	id := s.ident(r)
	sessions, err := s.Auth.Sessions(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, map[string]any{
			"id":           sess.ID,
			"label":        sess.Label,
			"created_at":   sess.CreatedAt,
			"last_used_at": sess.LastUsedAt,
			"expires_at":   sess.ExpiresAt,
			"revoked_at":   sess.RevokedAt,
			"current":      sess.ID == id.SessionID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (s *Server) handleSessionRevoke(w http.ResponseWriter, r *http.Request) {
	id := s.ident(r)
	if err := s.Auth.RevokeSession(r.Context(), id, r.PathValue("id")); err != nil {
		if errors.Is(err, index.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, "no such session")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- account management ------------------------------------------------------

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	id := s.ident(r)
	users, err := s.Auth.Users(r.Context(), id)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{"id": u.ID, "username": u.Username, "is_owner": u.IsOwner, "created_at": u.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	id := s.ident(r)
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	u, err := s.Auth.CreateUser(r.Context(), id, body.Username, body.Password)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": u.ID, "username": u.Username, "is_owner": u.IsOwner})
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	id := s.ident(r)
	if err := s.Auth.DeleteUser(r.Context(), id, r.PathValue("id")); err != nil {
		if errors.Is(err, index.ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "no such user")
			return
		}
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUserPassword(w http.ResponseWriter, r *http.Request) {
	id := s.ident(r)
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	if err := s.Auth.SetPassword(r.Context(), id, r.PathValue("id"), body.Password); err != nil {
		if errors.Is(err, index.ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "no such user")
			return
		}
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- helpers -------------------------------------------------------------

// writeAuthError maps the auth service's sentinel errors onto statuses.
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrNotOwner):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, auth.ErrBadUsername), errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, auth.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, auth.ErrReadOnly):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, index.ErrUserExists):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// decodeBody reads a small JSON body into v.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON")
		return err
	}
	return nil
}

func (s *Server) setRefreshCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshTokenCookie,
		Value:    token,
		Path:     "/api/auth",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshTokenCookie,
		Value:    "",
		Path:     "/api/auth",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// deviceLabel condenses a User-Agent into a human-readable session
// label.
func deviceLabel(ua string) string {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return "session"
	}
	if len(ua) > 100 {
		ua = ua[:100]
	}
	lower := strings.ToLower(ua)
	label := "browser"
	switch {
	case strings.Contains(lower, "android"):
		label = "android"
	case strings.Contains(lower, "iphone"), strings.Contains(lower, "ipad"):
		label = "ios"
	case strings.Contains(lower, "macintosh"):
		label = "mac"
	case strings.Contains(lower, "windows"):
		label = "windows"
	case strings.Contains(lower, "linux"):
		label = "linux"
	}
	if i := strings.Index(ua, "("); i >= 0 {
		if j := strings.Index(ua[i:], ")"); j > 0 {
			detail := ua[i+1 : i+j]
			if len(detail) > 60 {
				detail = detail[:60]
			}
			return label + " · " + detail
		}
	}
	return label
}
