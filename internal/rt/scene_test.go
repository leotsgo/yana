package rt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const drawingBody = "```excalidraw\n{\"type\":\"excalidraw\",\"elements\":[]}\n```\n"

// relaySub joins a drawing's room relay-only and returns the subd reply.
// It reads frames raw: there is no document to apply them to.
func relaySub(t *testing.T, p *peer, note string) ServerMessage {
	t.Helper()
	p.note = note
	p.send(ClientMessage{Type: msgSubscribe, Note: note, Author: p.author, Relay: true})
	return next(t, p, msgSubscribed)
}

// next returns the next frame of kind, skipping others.
func next(t *testing.T, p *peer, kind string) ServerMessage {
	t.Helper()
	deadline := time.After(waitFor)
	for {
		select {
		case m, ok := <-p.msgs:
			if !ok {
				t.Fatalf("%s: connection closed waiting for %s", p.author, kind)
			}
			if m.Type == kind {
				return m
			}
		case <-deadline:
			t.Fatalf("%s: no %s frame", p.author, kind)
		}
	}
}

// silent reports whether p hears nothing but chg for d: the watcher's
// notice of the note's creation may land late.
func silent(p *peer, d time.Duration) (ServerMessage, bool) {
	deadline := time.After(d)
	for {
		select {
		case m, ok := <-p.msgs:
			if !ok {
				return ServerMessage{}, true
			}
			if m.Type != msgChanged {
				return m, false
			}
		case <-deadline:
			return ServerMessage{}, true
		}
	}
}

// TestSceneRelayed: a drawing's room is relay-only. A dr frame reaches the
// room's other members as sent, never its sender, and never the
// reconciliation loop: no document is loaded, nothing is logged, the file
// is untouched. A change to the file reaches the room as chg with its hash.
func TestSceneRelayed(t *testing.T) {
	st := newStack(t, testHubOptions())
	rel := "alpha/Map.excalidraw.md"
	id := st.newNote(rel, drawingBody)
	before := st.fileBody(rel)

	a := newPeer(t, "user:ada")
	a.dial(st.url)
	defer a.close()
	if m := relaySub(t, a, id); len(m.Update) != 0 {
		t.Fatalf("relay-only subd carried a document: %d bytes", len(m.Update))
	}
	b := newPeer(t, "user:ben")
	b.dial(st.url)
	defer b.close()
	relaySub(t, b, id)
	if s := st.rec.Stats(); s.Loaded != 0 {
		t.Fatalf("a relay-only room loaded the document: %+v", s)
	}

	delta := []byte(`{"els":[{"id":"r1","type":"rectangle","version":2}]}`)
	a.send(ClientMessage{Type: msgScene, Note: id, Payload: delta})
	if m := next(t, b, msgScene); string(m.Payload) != string(delta) || m.Note != id {
		t.Fatalf("relayed dr = %+v", m)
	}
	if m, ok := silent(a, 250*time.Millisecond); !ok {
		t.Fatalf("ada heard %q after her own dr", m.Type)
	}
	if _, rows, err := st.db.LogStats(st.ctx, id); err != nil || rows != 0 {
		t.Fatalf("dr reached the log: rows=%d err=%v", rows, err)
	}
	if s := st.rec.Stats(); s.Loaded != 0 || s.Dirty != 0 {
		t.Fatalf("dr reached the reconciliation loop: %+v", s)
	}
	if st.fileBody(rel) != before {
		t.Fatal("dr changed the file")
	}

	// The file changes on disk (a save, an agent, git): both hear chg.
	changed := strings.Replace(drawingBody, "[]", `[{"id":"x"}]`, 1)
	content := "---\nid: " + id + "\ncreated: 2026-09-12T14:02:11Z\n---\n" + changed
	if err := os.WriteFile(st.abs(rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	for _, p := range []*peer{a, b} {
		// The notice of the note's creation may come first.
		for m := next(t, p, msgChanged); m.Hash != hex.EncodeToString(sum[:]); m = next(t, p, msgChanged) {
			if m.Note != id {
				t.Fatalf("%s: chg = %+v", p.author, m)
			}
		}
	}
}

// roles grants each author a fixed role; anyone else is refused.
type roles map[string]string

func (r roles) AuthorizeSubscribe(_ context.Context, author, _ string) (string, error) {
	if role, ok := r[author]; ok {
		return role, nil
	}
	return "", errors.New("not a member")
}

func (roles) NoteSpace(context.Context, string) (string, error) { return "alpha", nil }

// TestSceneWritersOnly: with accounts, a viewer receives dr frames but
// may not send them; a connection outside the room may not either. The
// frame is bounded by the inbound frame size like every other.
func TestSceneWritersOnly(t *testing.T) {
	st := newStack(t, testHubOptions())
	id := st.newNote("alpha/Map.excalidraw.md", drawingBody)
	hub := New(st.rec, roles{"user:ed": RoleEditor, "user:vic": RoleViewer}, Options{
		MaxMessageBytes: 4096,
		Verify: func(token string) (Identity, error) {
			return Identity{Username: token}, nil
		},
	}, testLogger(t))
	defer hub.Close()
	hs := httptest.NewServer(hub)
	defer hs.Close()
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws?token="

	ed := newPeer(t, "user:ed")
	ed.dial(url + "ed")
	defer ed.close()
	relaySub(t, ed, id)
	vic := newPeer(t, "user:vic")
	vic.dial(url + "vic")
	defer vic.close()
	relaySub(t, vic, id)

	vic.send(ClientMessage{Type: msgScene, Note: id, Payload: []byte(`{"els":[]}`)})
	if m := next(t, vic, msgError); m.Code != errForbidden {
		t.Fatalf("viewer dr: %+v", m)
	}
	if m, ok := silent(ed, 250*time.Millisecond); !ok {
		t.Fatalf("a viewer's dr reached the editor: %+v", m)
	}
	ed.send(ClientMessage{Type: msgScene, Note: id, Payload: []byte(`{"els":[1]}`)})
	if m := next(t, vic, msgScene); string(m.Payload) != `{"els":[1]}` {
		t.Fatalf("viewer got %+v", m)
	}

	// Not subscribed: refused.
	out := newPeer(t, "user:ed")
	out.dial(url + "ed")
	defer out.close()
	out.send(ClientMessage{Type: msgScene, Note: id, Payload: []byte(`{}`)})
	if m := next(t, out, msgError); m.Code != errForbidden {
		t.Fatalf("non-member dr: %+v", m)
	}

	// Over the frame limit: the connection closes, the room carries on.
	ed.send(ClientMessage{Type: msgScene, Note: id, Payload: make([]byte, 8192)})
	if !st.eventually(waitFor, func() bool { return hub.Stats().Subscriptions == 1 }) {
		t.Fatalf("an oversized dr did not close its connection: %+v", hub.Stats())
	}
	if m, ok := silent(vic, 250*time.Millisecond); !ok {
		t.Fatalf("an oversized dr was relayed: %+v", m)
	}
}
