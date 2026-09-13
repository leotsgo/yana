package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/rt"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// TestRelayMountedInServerMux checks the /ws wiring through the real server
// routes and that /api/status carries the relay counters.
func TestRelayMountedInServerMux(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	root, err := pathsafe.NewRoot(dir, pathsafe.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(filepath.Join(dir, ".sync", "index.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sc := scanner.New(root, db, scanner.Options{SettleTime: time.Millisecond}, log)
	rec := reconcile.New(root, db, sc, reconcile.Options{UnloadAfter: -1}, log)
	if err := rec.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := rec.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := sc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	hub := rt.New(rec, nil, rt.Options{}, log)
	t.Cleanup(hub.Close)

	srv := New(Deps{DB: db, Root: root, Log: log, Version: "test", Sync: rec, RT: hub})
	srv.SetReady(true)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// Write a note directly and index it so the relay has something to
	// subscribe to.
	id := scanner.NewID(time.Now())
	note := "---\nid: " + id + "\ncreated: 2026-09-13T00:00:00Z\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sc.ScanOne(context.Background(), "note.md"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "bye")

	sub := struct {
		Type   string `msgpack:"t"`
		Note   string `msgpack:"n"`
		SV     []byte `msgpack:"sv"`
		Author string `msgpack:"a"`
	}{Type: "sub", Note: id, Author: "user:ada"}
	w, err := ws.Writer(ctx, websocket.MessageBinary)
	if err != nil {
		t.Fatal(err)
	}
	if err := msgpack.NewEncoder(w).Encode(&sub); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	var reply struct {
		Type   string `msgpack:"t"`
		Update []byte `msgpack:"u"`
	}
	if err := wsRead(ctx, ws, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Type != "subd" || len(reply.Update) == 0 {
		t.Fatalf("unexpected subscribe reply: %+v", reply)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(ts.URL + "/api/status")
		if err != nil {
			t.Fatal(err)
		}
		var status struct {
			Realtime *struct {
				Connections int `json:"connections"`
				Rooms       int `json:"rooms"`
			} `json:"realtime"`
			Sync *struct {
				Dirty int `json:"dirty"`
			} `json:"sync"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			resp.Body.Close()
			t.Fatal(err)
		}
		resp.Body.Close()
		if status.Realtime != nil && status.Realtime.Connections == 1 && status.Realtime.Rooms == 1 &&
			status.Sync != nil && status.Sync.Dirty >= 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("realtime counters never appeared in /api/status")
}

func wsRead(ctx context.Context, ws *websocket.Conn, out any) error {
	typ, r, err := ws.Reader(ctx)
	if err != nil {
		return err
	}
	if typ != websocket.MessageBinary {
		t := "frame is not binary"
		return &websocket.CloseError{Code: websocket.StatusUnsupportedData, Reason: t}
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return msgpack.Unmarshal(data, out)
}
