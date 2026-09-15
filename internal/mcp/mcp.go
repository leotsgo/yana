// Package mcp is the agent tool surface: a Model Context Protocol server
// exposed at POST /mcp using the Streamable HTTP transport, answering
// JSON-RPC 2.0. It exists so an agent that is not on the box can list,
// read, write, and move notes with the same safety and attribution as
// every other writer: paths go through pathsafe, writes go through the
// reconciliation loop as operations authored "agent:<label>", and the
// agent rate limit applies.
//
// The protocol surface is intentionally small — initialize, ping,
// tools/list, tools/call — implemented directly rather than through an
// SDK. Sessions are not used; each POST is independent.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/madeofpendletonwool/yana/internal/auth"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// ProtocolVersion is the newest spec this server speaks; the versions a
// client may ask for are negotiated in initialize.
const (
	ProtocolVersion  = "2025-06-18"
	serverName       = "yana"
	protocolVersion1 = "2025-03-26"
)

// Deps are everything the tools need.
type Deps struct {
	DB *index.DB
	// Root is the path-safety root; every caller-supplied path goes
	// through it.
	Root *pathsafe.Root
	// Sync is the reconciliation loop; nil disables the write tools.
	Sync *reconcile.Reconciler
	// Scanner indexes new note files.
	Scanner *scanner.Scanner
	// Auth verifies agent tokens; nil answers every request with 503.
	Auth *auth.Service
	// Limit bounds writes per author; nil allows everything (tests).
	Limit *pathsafe.RateLimiter
	Log   *slog.Logger
	// Version is reported in serverInfo.
	Version string
}

// Handler serves POST /mcp.
type Handler struct {
	Deps
}

// New builds the handler.
func New(d Deps) *Handler {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	d.Log = d.Log.With("component", "mcp")
	return &Handler{Deps: d}
}

// --- JSON-RPC -------------------------------------------------------------

// rpcMessage is one inbound message, request or notification.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is one outbound answer to a request.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC error codes from the MCP spec.
const (
	codeParse     = -32700
	codeInvalid   = -32600
	codeMethod    = -32601
	codeInvalidP  = -32602
	codeInternal  = -32603
	codeForbidden = -320402
)

func errResponse(id json.RawMessage, code int, msg string) *rpcResponse {
	if id == nil {
		id = json.RawMessage("null")
	}
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func resultResponse(id json.RawMessage, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

// ServeHTTP implements the Streamable HTTP transport. POST carries one
// message or a batch; requests answer application/json, notifications
// answer 202 alone. Other methods are 405 — there is no server-initiated
// stream and no session to delete.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "the MCP endpoint speaks POST only", http.StatusMethodNotAllowed)
		return
	}
	if h.Auth == nil {
		http.Error(w, "this server runs without accounts and has no agent tokens", http.StatusServiceUnavailable)
		return
	}
	secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	agent, err := h.Auth.VerifyAgentToken(r.Context(), secret)
	if err != nil {
		http.Error(w, "a valid agent token is required (Authorization: Bearer ya_...)", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
	if err != nil {
		writeRPC(w, http.StatusBadRequest, errResponse(nil, codeParse, "could not read the request body"))
		return
	}
	var single rpcMessage
	if err := json.Unmarshal(body, &single); err == nil && single.Method != "" {
		h.serveOne(w, r, agent, single)
		return
	}
	var batch []rpcMessage
	if err := json.Unmarshal(body, &batch); err != nil || len(batch) == 0 {
		writeRPC(w, http.StatusBadRequest, errResponse(nil, codeParse, "the body must be one JSON-RPC message or a batch"))
		return
	}
	out := make([]*rpcResponse, 0, len(batch))
	for _, msg := range batch {
		if msg.ID == nil {
			continue // notification inside a batch: no answer
		}
		if resp := h.dispatch(r.Context(), agent, msg); resp != nil {
			out = append(out, resp)
		}
	}
	if len(out) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeRPC(w, http.StatusOK, out)
}

// serveOne answers one message.
func (h *Handler) serveOne(w http.ResponseWriter, r *http.Request, agent auth.AgentIdentity, msg rpcMessage) {
	if msg.JSONRPC != "2.0" {
		writeRPC(w, http.StatusOK, errResponse(msg.ID, codeInvalid, "jsonrpc must be \"2.0\""))
		return
	}
	if msg.ID == nil {
		// A notification. The only one in this surface needs no work.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	resp := h.dispatch(r.Context(), agent, msg)
	if resp == nil {
		resp = errResponse(msg.ID, codeInternal, "no answer produced")
	}
	writeRPC(w, http.StatusOK, resp)
}

// dispatch routes one request. A nil return means "answered elsewhere",
// which does not happen for the methods this server implements.
func (h *Handler) dispatch(ctx context.Context, agent auth.AgentIdentity, msg rpcMessage) *rpcResponse {
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		return resultResponse(msg.ID, h.initialize(p.ProtocolVersion))
	case "ping":
		return resultResponse(msg.ID, map[string]any{})
	case "tools/list":
		return resultResponse(msg.ID, map[string]any{"tools": toolList()})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil || p.Name == "" {
			return errResponse(msg.ID, codeInvalidP, "params must name a tool")
		}
		if !knownTool(p.Name) {
			return errResponse(msg.ID, codeInvalidP, "unknown tool: "+p.Name)
		}
		res := h.callTool(ctx, agent, p.Name, p.Arguments)
		return resultResponse(msg.ID, res)
	default:
		return errResponse(msg.ID, codeMethod, "no such method: "+msg.Method)
	}
}

// initialize negotiates the protocol version and describes the server.
func (h *Handler) initialize(requested string) map[string]any {
	version := ProtocolVersion
	if requested == ProtocolVersion || requested == protocolVersion1 {
		version = requested
	}
	tools := map[string]any{"tools": map[string]any{"listChanged": false}}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    tools,
		"serverInfo":      map[string]string{"name": serverName, "version": h.Version},
		"instructions": strings.Join([]string{
			"Read and write notes in the spaces this token is scoped to.",
			"Paths are relative and forward-slashed; a note is identified by id or path.",
			"Write through write_note or append_note so edits merge with concurrent human typing instead of overwriting it.",
			"Link notes with [[wikilinks]] within one space; the conventions file in each space describes its structure.",
			"Every write is attributed to agent:<label> in the note history and git.",
		}, " "),
	}
}

// writeRPC writes one JSON-RPC body.
func writeRPC(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

var errNoSync = errors.New("this server runs without the reconciliation loop; write tools are unavailable")
