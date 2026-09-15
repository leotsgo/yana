// Agent token management: mint, list, and revoke the credentials agents
// present at /mcp. Owner-only, like account management, because a token
// is a grant of write access. The secret appears once, at creation.
package server

import (
	"errors"
	"net/http"

	"github.com/madeofpendletonwool/yana/internal/auth"
	"github.com/madeofpendletonwool/yana/internal/index"
)

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.Auth.ListAgentTokens(r.Context(), s.ident(r))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if tokens == nil {
		tokens = []index.AgentToken{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": tokens})
}

func (s *Server) handleAgentCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label    string   `json:"label"`
		Spaces   []string `json:"spaces"`
		CanWrite bool     `json:"can_write"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	tok, err := s.Auth.CreateAgentToken(r.Context(), s.ident(r), body.Label, body.Spaces, body.CanWrite)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": tok.ID, "label": tok.Label, "spaces": tok.Spaces,
		"can_write": tok.CanWrite, "token": tok.Secret,
	})
}

func (s *Server) handleAgentDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Auth.RevokeAgentToken(r.Context(), s.ident(r), r.PathValue("id")); err != nil {
		if errors.Is(err, index.ErrAgentTokenNotFound) {
			writeError(w, http.StatusNotFound, "no such agent token")
			return
		}
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// writeAgentError maps the agent token surface's errors onto statuses.
func writeAgentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrNotOwner):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, auth.ErrBadLabel), errors.Is(err, auth.ErrBadScope):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, index.ErrAgentLabelTaken):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}
