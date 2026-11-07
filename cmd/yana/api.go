// `yana api <verb>` is the neovim plugin's backend: the plugin never
// speaks HTTP or holds a token, it runs these and reads JSON. Each verb
// prints one JSON value on stdout; search and watch print one per line.
// A failure prints {"error": "...", "status": <http status or 0>} on
// stdout and exits 1.
//
//	whoami                       {server, host, username}
//	tree                         {server, host, entries: [entry]}
//	read <id> [md|html]          {id, path, kind, role, text, base|hash}
//	write <id>                   stdin {kind, text, base|hash}; md answers
//	                             {id, path, text, base} with the merged text,
//	                             html {hash, conflict_copy}
//	search <query>               one {id, path, title, kind, snippet} per line
//	daily <YYYY-MM-DD>           the daily note's entry, made when missing
//	create <path>                a note (.md added when the name has no
//	                             note extension), or a folder when path ends
//	                             in / (a space at the top)
//	move <id | folder/> <to>     {path}
//	delete <id | folder/>        {} (notes and folders go to the trash)
//	resolve <from-path> <target> the entry a [[target]] in from-path names
//	recent [--push id]           {ids}, most recent first
//	watch <space>...             one {id, path, hash} per change, one
//	                             {watching: space} per subscription; runs
//	                             until stdin closes
//	logout                       {}
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/leotsgo/yana/internal/scanner"
	"github.com/leotsgo/yana/internal/wikilink"
)

func apiMain(args []string, stdin io.Reader, stdout io.Writer) int {
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	err := func() error {
		if len(args) == 0 {
			return errors.New("usage: yana api <verb> [args]")
		}
		s, err := loadSession()
		if err != nil {
			return err
		}
		out, err := apiVerb(s, args[0], args[1:], stdin, enc)
		if err == nil && out != nil {
			err = enc.Encode(out)
		}
		return err
	}()
	if err != nil {
		status := 0
		var ae *apiError
		if errors.As(err, &ae) {
			status = ae.Status
		}
		_ = enc.Encode(map[string]any{"error": err.Error(), "status": status})
		return 1
	}
	return 0
}

// need checks a verb's argument count.
func need(args []string, n int, usage string) error {
	if len(args) != n {
		return errors.New("usage: yana api " + usage)
	}
	return nil
}

func hostOf(server string) string {
	if u, err := url.Parse(server); err == nil && u.Host != "" {
		return u.Host
	}
	return server
}

// apiVerb runs one verb. It answers the value to print, or prints its
// own lines to enc and answers nil.
func apiVerb(s *session, verb string, args []string, stdin io.Reader, enc *json.Encoder) (any, error) {
	esc := url.PathEscape
	switch verb {
	case "whoami":
		var res struct {
			User struct {
				Username string `json:"username"`
			} `json:"user"`
		}
		if err := s.do(http.MethodGet, "/api/auth/state", nil, &res); err != nil {
			return nil, err
		}
		return map[string]string{"server": s.Server, "host": hostOf(s.Server), "username": res.User.Username}, nil

	case "tree":
		all, err := fetchTree(s)
		if err != nil {
			return nil, err
		}
		if all == nil {
			all = []entry{}
		}
		return map[string]any{"server": s.Server, "host": hostOf(s.Server), "entries": all}, nil

	case "read":
		if len(args) < 1 || len(args) > 2 {
			return nil, errors.New("usage: yana api read <id> [md|html]")
		}
		id, kind := args[0], "md"
		if len(args) == 2 {
			kind = args[1]
		}
		switch kind {
		case "md":
			t, err := fetchText(s, id)
			if err != nil {
				return nil, err
			}
			return map[string]any{"id": t.ID, "path": t.Path, "kind": "md", "role": t.Role, "text": t.Text, "base": t.Base}, nil
		case "html":
			var n struct {
				Path   string `json:"path"`
				Source string `json:"source"`
				Hash   string `json:"content_hash"`
				Role   string `json:"role"`
			}
			if err := s.do(http.MethodGet, "/api/notes/"+esc(id), nil, &n); err != nil {
				return nil, err
			}
			return map[string]any{"id": id, "path": n.Path, "kind": "html", "role": n.Role, "text": n.Source, "hash": n.Hash}, nil
		}
		return nil, fmt.Errorf("a %s is not edited as text; open it in the app: %s", kind, noteURL(s, id))

	case "write":
		if err := need(args, 1, "write <id> < {kind, text, base|hash}"); err != nil {
			return nil, err
		}
		var body struct {
			Kind string  `json:"kind"`
			Text *string `json:"text"`
			Base []byte  `json:"base"`
			Hash string  `json:"hash"`
		}
		if err := json.NewDecoder(stdin).Decode(&body); err != nil || body.Text == nil {
			return nil, errors.New("stdin must be JSON with a text field")
		}
		if body.Kind == "html" {
			var res struct {
				Hash     string `json:"hash"`
				Conflict string `json:"conflict_copy"`
			}
			err := s.do(http.MethodPut, "/api/notes/"+esc(args[0])+"/source", map[string]string{"source": *body.Text, "base_hash": body.Hash}, &res)
			if err != nil {
				return nil, err
			}
			return map[string]string{"hash": res.Hash, "conflict_copy": res.Conflict}, nil
		}
		t, err := saveText(s, args[0], body.Base, *body.Text)
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": t.ID, "path": t.Path, "text": t.Text, "base": t.Base}, nil

	case "search":
		if len(args) == 0 || strings.TrimSpace(strings.Join(args, " ")) == "" {
			return nil, errors.New("usage: yana api search <query>")
		}
		var res struct {
			Hits []struct {
				Note struct {
					ID    string `json:"id"`
					Path  string `json:"path"`
					Title string `json:"title"`
					Kind  string `json:"kind"`
				} `json:"note"`
				Snippet string `json:"snippet"`
			} `json:"hits"`
		}
		if err := s.do(http.MethodGet, "/api/search?limit=50&q="+url.QueryEscape(strings.Join(args, " ")), nil, &res); err != nil {
			return nil, err
		}
		for _, h := range res.Hits {
			n := h.Note
			if err := enc.Encode(map[string]string{"id": n.ID, "path": n.Path, "title": n.Title, "kind": n.Kind, "snippet": h.Snippet}); err != nil {
				return nil, err
			}
		}
		return nil, nil

	case "daily":
		if err := need(args, 1, "daily <YYYY-MM-DD>"); err != nil {
			return nil, err
		}
		d, err := time.ParseInLocation("2006-01-02", args[0], time.Local)
		if err != nil {
			return nil, errors.New("the date must be YYYY-MM-DD")
		}
		return dailyNote(s, "", d)

	case "create":
		if err := need(args, 1, "create <path>"); err != nil {
			return nil, err
		}
		return create(s, args[0])

	case "move":
		if err := need(args, 2, "move <id | folder/> <to>"); err != nil {
			return nil, err
		}
		from, to := args[0], strings.Trim(args[1], "/")
		if dir, ok := strings.CutSuffix(from, "/"); ok {
			if !strings.Contains(dir, "/") {
				return nil, errors.New("spaces are renamed in the app's settings")
			}
			return map[string]string{"path": to}, s.do(http.MethodPost, "/api/dirs/move", map[string]string{"path": dir, "to": to}, nil)
		}
		if scanner.KindOf(to) == "" {
			// A new name without an extension keeps the note's kind.
			var n struct {
				Path string `json:"path"`
			}
			if err := s.do(http.MethodGet, "/api/notes/"+esc(from), nil, &n); err != nil {
				return nil, err
			}
			to += noteExt(n.Path)
		}
		return map[string]string{"path": to}, s.do(http.MethodPost, "/api/notes/"+esc(from)+"/move", map[string]string{"path": to}, nil)

	case "delete":
		if err := need(args, 1, "delete <id | folder/>"); err != nil {
			return nil, err
		}
		if dir, ok := strings.CutSuffix(args[0], "/"); ok {
			if !strings.Contains(dir, "/") {
				return nil, errors.New("spaces are deleted in the app's settings")
			}
			return struct{}{}, s.do(http.MethodDelete, "/api/dirs?path="+url.QueryEscape(dir), nil, nil)
		}
		return struct{}{}, s.do(http.MethodDelete, "/api/notes/"+esc(args[0]), nil, nil)

	case "resolve":
		if err := need(args, 2, "resolve <from-path> <target>"); err != nil {
			return nil, err
		}
		all, err := fetchTree(s)
		if err != nil {
			return nil, err
		}
		if e, ok := resolveLink(all, args[0], args[1]); ok {
			return e, nil
		}
		return nil, &apiError{Status: http.StatusNotFound, Msg: "no note named " + args[1]}

	case "recent":
		ids := loadRecent()
		if len(args) == 2 && args[0] == "--push" {
			noteOpened(args[1])
			ids = pushRecent(ids, args[1])
		} else if len(args) != 0 {
			return nil, errors.New("usage: yana api recent [--push id]")
		}
		if ids == nil {
			ids = []string{}
		}
		return map[string][]string{"ids": ids}, nil

	case "watch":
		if len(args) == 0 {
			return nil, errors.New("usage: yana api watch <space>...")
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() { // the plugin going away closes stdin
			_, _ = io.Copy(io.Discard, stdin)
			cancel()
		}()
		return nil, watch(ctx, s, args, enc)

	case "logout":
		return struct{}{}, logout()
	}
	return nil, fmt.Errorf("no api verb %q", verb)
}

// noteExt is the note extension p ends in: .md, .html, .excalidraw.md.
func noteExt(p string) string {
	if strings.HasSuffix(p, ".excalidraw.md") {
		return ".excalidraw.md"
	}
	return path.Ext(p)
}

// create makes the note at p, or the folder when p ends in a slash; a
// folder at the top is a new space.
func create(s *session, p string) (entry, error) {
	if dir, ok := strings.CutSuffix(p, "/"); ok {
		dir = strings.Trim(dir, "/")
		var err error
		if !strings.Contains(dir, "/") {
			err = s.do(http.MethodPost, "/api/spaces", map[string]string{"name": dir}, nil)
		} else {
			err = s.do(http.MethodPost, "/api/dirs", map[string]string{"path": dir}, nil)
		}
		return entry{Path: dir, Name: path.Base(dir), Dir: true}, err
	}
	p = strings.Trim(p, "/")
	if scanner.KindOf(p) == "" {
		p += ".md"
	}
	var res struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := s.do(http.MethodPost, "/api/notes", map[string]string{"path": p}, &res); err != nil {
		return entry{}, err
	}
	return entry{Path: res.Path, Name: path.Base(res.Path), ID: res.ID, Kind: scanner.KindOf(res.Path)}, nil
}

// resolveLink finds the note a [[target]] written in the note at from
// names, the way the server resolves links: within from's space only.
func resolveLink(all []entry, from, target string) (entry, bool) {
	target, _, _ = strings.Cut(target, "|")
	target, _, _ = strings.Cut(target, "#")
	space, _, ok := strings.Cut(from, "/")
	if !ok {
		space = ""
	}
	var refs []wikilink.NoteRef
	byID := map[string]entry{}
	for _, e := range all {
		if e.Dir {
			continue
		}
		if sp, _, ok := strings.Cut(e.Path, "/"); (ok && sp == space) || (!ok && space == "") {
			refs = append(refs, wikilink.NoteRef{ID: e.ID, RelPath: e.Path, Aliases: e.Aliases})
			byID[e.ID] = e
		}
	}
	res := wikilink.NewResolver(space, refs).Resolve(target, from)
	e, found := byID[res.ToID]
	return e, res.OK && found
}

// --- watch ---------------------------------------------------------------------

type wsIn struct {
	Type   string `msgpack:"t"`
	Note   string `msgpack:"n"`
	Path   string `msgpack:"path"`
	Hash   string `msgpack:"h"`
	Space  string `msgpack:"s"`
	Code   string `msgpack:"c"`
	Reason string `msgpack:"r"`
}

// watch hears about changes in spaces through the realtime relay's
// watch subscription, printing one line per change, and reconnects with
// backoff until ctx ends or the session does.
func watch(ctx context.Context, s *session, spaces []string, enc *json.Encoder) error {
	wait := 500 * time.Millisecond
	for {
		start := time.Now()
		err := watchOnce(ctx, s, spaces, enc)
		if ctx.Err() != nil {
			return nil
		}
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
			return err
		}
		if time.Since(start) > time.Minute {
			wait = 500 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		wait = min(2*wait, 8*time.Second)
	}
}

func watchOnce(ctx context.Context, s *session, spaces []string, enc *json.Encoder) error {
	u := strings.Replace(strings.TrimRight(s.Server, "/"), "http", "ws", 1) + "/ws"
	dial := func() (*websocket.Conn, *http.Response, error) {
		return websocket.Dial(ctx, u+"?token="+url.QueryEscape(s.Access), nil)
	}
	c, resp, err := dial()
	if resp != nil && resp.StatusCode == http.StatusUnauthorized {
		if err := s.refresh(); err != nil {
			return &apiError{Status: http.StatusUnauthorized, Msg: err.Error()}
		}
		c, _, err = dial()
	}
	if err != nil {
		return err
	}
	defer c.CloseNow()
	for _, sp := range spaces {
		raw, _ := msgpack.Marshal(map[string]string{"t": "watch", "s": sp})
		if err := c.Write(ctx, websocket.MessageBinary, raw); err != nil {
			return err
		}
	}
	for {
		_, raw, err := c.Read(ctx)
		if err != nil {
			return err
		}
		var m wsIn
		if msgpack.Unmarshal(raw, &m) != nil {
			continue
		}
		switch m.Type {
		case "chg":
			err = enc.Encode(map[string]string{"id": m.Note, "path": m.Path, "hash": m.Hash})
		case "watchd":
			err = enc.Encode(map[string]string{"watching": m.Space})
		case "err":
			err = enc.Encode(map[string]string{"error": m.Code + ": " + m.Reason})
		}
		if err != nil { // stdout is unbuffered: each line reaches the plugin as printed
			return err
		}
	}
}
