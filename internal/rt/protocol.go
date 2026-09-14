package rt

import "regexp"

// Message kinds on the wire. Client-to-server kinds are requested actions;
// server-to-client kinds are relay output and replies. The full protocol is
// documented in docs/realtime.md.
const (
	msgSubscribe   = "sub" // client → server: join a note's room
	msgUnsubscribe = "unsub"
	msgUpdate      = "upd" // CRDT payload, relayed and recorded
	msgAwareness   = "aw"  // presence payload, relayed only
	msgPing        = "ping"

	msgSubscribed = "subd" // reply to sub: the update the client is missing
	msgPong       = "pong"
	msgMoved      = "moved"   // the note's file moved
	msgDeleted    = "deleted" // the note's file is gone
	msgError      = "err"
)

// Error codes carried by err messages.
const (
	errNotFound      = "not_found"
	errInvalidAuthor = "invalid_author"
	errRateLimited   = "rate_limited"
	errTooManyRooms  = "too_many_rooms"
	errInvalid       = "invalid"
	errInternal      = "internal"
	errForbidden     = "forbidden"
)

// ClientMessage is one framed message from a client. Every field except
// Type is optional and read per message kind; unknown fields are ignored so
// the protocol can grow.
type ClientMessage struct {
	Type string `msgpack:"t"`
	Note string `msgpack:"n,omitempty"`
	// SV is the client's state vector for sub: the server replies with
	// everything it is missing. Empty means "send the whole document".
	SV []byte `msgpack:"sv,omitempty"`
	// Update is the opaque CRDT payload for upd. The relay never looks
	// inside it.
	Update []byte `msgpack:"u,omitempty"`
	// Author is the sender's identity for upd ("user:<name>" or
	// "agent:<label>") and optionally declares it for sub.
	Author string `msgpack:"a,omitempty"`
	// Payload is the opaque awareness state for aw: cursor, selection,
	// user colour. Relayed as-is, never persisted.
	Payload []byte `msgpack:"p,omitempty"`
}

// ServerMessage is one framed message to a client.
type ServerMessage struct {
	Type string `msgpack:"t"`
	Note string `msgpack:"n,omitempty"`
	// Update is the opaque CRDT payload for subd (the delta the client is
	// missing) and upd (a relayed update).
	Update []byte `msgpack:"u,omitempty"`
	Author string `msgpack:"a,omitempty"`
	// Payload is the opaque awareness state for aw.
	Payload []byte `msgpack:"p,omitempty"`
	// Path is the note's new relative path for moved.
	Path   string `msgpack:"path,omitempty"`
	Code   string `msgpack:"c,omitempty"`
	Reason string `msgpack:"r,omitempty"`
}

// authorPattern is the shape of an author identity: "user:<name>" or
// "agent:<label>". It matches the strings the CRDT log and the rate limiter
// key on. The filesystem author is produced by the reconciliation loop and
// may not be claimed by a client.
var authorPattern = regexp.MustCompile(`^(user|agent):[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func validAuthor(author string) bool {
	return author != "filesystem" && authorPattern.MatchString(author)
}

func validNoteID(id string) bool {
	if len(id) != 26 {
		return false
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
			return false
		}
	}
	return true
}
