// The notes side of the command line's client: the tree, a note's text
// and its saves, and the editor session behind `yana open` and the TUI.
//
//	yana open <path or id>   edit one note in $VISUAL / $EDITOR (nvim)
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// entry is one row of the tree, folders and notes alike, by full path.
// A space is a folder at the top.
type entry struct {
	Path  string   `json:"path"`
	Name  string   `json:"name"`
	Dir   bool     `json:"dir,omitempty"`
	ID    string   `json:"id,omitempty"`
	Title string   `json:"title,omitempty"`
	Kind  string   `json:"kind,omitempty"`
	Tags  []string `json:"tags,omitempty"`
}

type treeNode struct {
	Type     string     `json:"type"`
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Kind     string     `json:"kind"`
	Tags     []string   `json:"tags"`
	Children []treeNode `json:"children"`
}

type spaceTree struct {
	Name     string     `json:"name"`
	Children []treeNode `json:"children"`
}

// fetchTree lists every folder and note the account can see.
func fetchTree(s *session) ([]entry, error) {
	var res struct {
		Spaces []spaceTree `json:"spaces"`
	}
	if err := s.do(http.MethodGet, "/api/tree", nil, &res); err != nil {
		return nil, err
	}
	return flatten(res.Spaces), nil
}

// flatten turns the server's nested tree into one list. Notes loose in the
// root (the space named "") sit at the top beside the spaces; a conflict
// copy, which the server nests under its note, sits beside it.
func flatten(spaces []spaceTree) []entry {
	var out []entry
	var walk func([]treeNode)
	walk = func(nodes []treeNode) {
		for _, n := range nodes {
			if n.Type == "dir" {
				out = append(out, entry{Path: n.Path, Name: n.Name, Dir: true})
			} else {
				out = append(out, entry{Path: n.Path, Name: n.Name, ID: n.ID, Title: n.Title, Kind: n.Kind, Tags: n.Tags})
			}
			walk(n.Children)
		}
	}
	for _, sp := range spaces {
		if sp.Name != "" {
			out = append(out, entry{Path: sp.Name, Name: sp.Name, Dir: true})
		}
		walk(sp.Children)
	}
	return out
}

// parentOf is the folder holding p; "" is the top.
func parentOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

// listDir is one folder's listing: folders first, then notes, by name.
func listDir(all []entry, dir string) []entry {
	var out []entry
	for _, e := range all {
		if parentOf(e.Path) == dir {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func looksLikeID(s string) bool {
	if len(s) != 26 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
			return false
		}
	}
	return true
}

// resolveNote finds a note by id, by path, or by path without its .md.
func resolveNote(all []entry, arg string) (entry, bool) {
	arg = strings.Trim(arg, "/")
	for _, e := range all {
		if !e.Dir && (e.ID == arg || e.Path == arg || e.Path == arg+".md") {
			return e, true
		}
	}
	return entry{}, false
}

// noteText is a markdown note's body and the document state it was read
// from, which the next save names as its base.
type noteText struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Role string `json:"role"`
	Text string `json:"text"`
	Base []byte `json:"base"`
}

func fetchText(s *session, id string) (*noteText, error) {
	var t noteText
	err := s.do(http.MethodGet, "/api/notes/"+url.PathEscape(id)+"/text", nil, &t)
	return &t, err
}

// saveText sends text edited from base. The server merges it with what
// changed since; the answer's base is the one for the next save.
func saveText(s *session, id string, base []byte, text string) (*noteText, error) {
	var t noteText
	err := s.do(http.MethodPut, "/api/notes/"+url.PathEscape(id)+"/text", map[string]any{"base": base, "text": text}, &t)
	return &t, err
}

// noteURL is where the app shows a note, for what the terminal cannot
// edit (drawings).
func noteURL(s *session, id string) string { return s.Server + "/n/" + id }

// --- the editor session ----------------------------------------------------

// editSession is one note open in the editor. The note's text sits in a
// private temp file; every write the editor makes is sent to the server
// as it lands, not only when the editor quits.
type editSession struct {
	s        *session
	note     entry
	dir      string // the temp dir, removed on a clean finish
	file     string
	readOnly bool

	mu    sync.Mutex
	last  string // what the server has from us
	base  []byte // markdown: the document state last saved from
	hash  string // html: the file hash last saved from
	err   error  // the last save's failure, kept until a save succeeds
	saves int
	stop  chan struct{}
	done  chan struct{}
}

// startEdit fetches the note, writes it out and returns the editor's
// command, ready to run. at, when set, picks the line the cursor starts
// on from the text.
func startEdit(s *session, note entry, at func(string) int) (*editSession, *exec.Cmd, error) {
	es := &editSession{s: s, note: note, stop: make(chan struct{}), done: make(chan struct{})}
	switch note.Kind {
	case "md":
		t, err := fetchText(s, note.ID)
		if err != nil {
			return nil, nil, err
		}
		es.last, es.base, es.readOnly = t.Text, t.Base, t.Role == "viewer"
	case "html":
		var n struct {
			Source string `json:"source"`
			Hash   string `json:"content_hash"`
			Role   string `json:"role"`
		}
		if err := s.do(http.MethodGet, "/api/notes/"+url.PathEscape(note.ID), nil, &n); err != nil {
			return nil, nil, err
		}
		es.last, es.hash, es.readOnly = n.Source, n.Hash, n.Role == "viewer"
	default:
		return nil, nil, fmt.Errorf("%s is a %s; open it in the app: %s", note.Path, note.Kind, noteURL(s, note.ID))
	}
	dir, err := os.MkdirTemp("", "yana-")
	if err != nil {
		return nil, nil, err
	}
	es.dir = dir
	// Named after the note, so the editor picks the filetype from it.
	es.file = filepath.Join(dir, path.Base(note.Path))
	if err := os.WriteFile(es.file, []byte(es.last), 0o600); err != nil {
		os.RemoveAll(dir)
		return nil, nil, err
	}
	line := 0
	if at != nil {
		line = at(es.last)
	}
	cmd := editorCmd(es.file, line, es.readOnly)
	if es.readOnly {
		close(es.done)
	} else {
		go es.watch()
	}
	return es, cmd, nil
}

// configuredEditor is the config file's editor, which wins over the
// environment's.
var configuredEditor string

// editorCmd is the configured editor, else $VISUAL, else $EDITOR, else
// nvim, on file. The +line and
// -R flags are vim's, which nano and emacs share for the line.
func editorCmd(file string, line int, readOnly bool) *exec.Cmd {
	ed := configuredEditor
	if ed == "" {
		ed = os.Getenv("VISUAL")
	}
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	if ed == "" {
		ed = "nvim"
	}
	args := strings.Fields(ed)
	name := filepath.Base(args[0])
	vimlike := name == "vi" || name == "vim" || name == "nvim"
	if line > 0 && (vimlike || name == "nano" || name == "emacs") {
		args = append(args, fmt.Sprintf("+%d", line))
	}
	if readOnly && vimlike {
		args = append(args, "-R")
	}
	args = append(args, file)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd
}

// watch saves the file whenever its content changes.
// ponytail: polls every 300ms; fsnotify if that ever shows up in a profile.
func (es *editSession) watch() {
	defer close(es.done)
	t := time.NewTicker(300 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-es.stop:
			return
		case <-t.C:
			es.sync()
		}
	}
}

// sync sends the file to the server if it changed since the last save.
func (es *editSession) sync() {
	raw, err := os.ReadFile(es.file)
	if err != nil {
		return // mid-write by the editor; the next tick sees it
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	text := string(raw)
	if text == es.last && es.err == nil {
		return
	}
	if es.note.Kind == "md" {
		var t *noteText
		if t, err = saveText(es.s, es.note.ID, es.base, text); err == nil {
			es.base = t.Base
		}
	} else {
		var res struct {
			Hash     string `json:"hash"`
			Conflict string `json:"conflict_copy"`
		}
		body := map[string]string{"source": text, "base_hash": es.hash}
		if err = es.s.do(http.MethodPut, "/api/notes/"+url.PathEscape(es.note.ID)+"/source", body, &res); err == nil {
			es.hash = res.Hash
		}
	}
	es.err = err
	if err == nil {
		es.last = text
		es.saves++
	}
}

// finish stops the watcher, sends the last write and removes the temp
// file. When a save failed the file stays, and the error says where.
func (es *editSession) finish() (saves int, err error) {
	if !es.readOnly {
		close(es.stop)
		<-es.done
		es.sync()
	}
	if es.err != nil {
		return es.saves, fmt.Errorf("could not save %s (%v); your text is kept in %s", es.note.Path, es.err, es.file)
	}
	os.RemoveAll(es.dir)
	return es.saves, nil
}

// --- recently opened ----------------------------------------------------------

const recentMax = 50

// loadRecent returns note ids, most recent first.
func loadRecent() []string {
	p, err := statePath("recent.json")
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var ids []string
	_ = json.Unmarshal(raw, &ids)
	return ids
}

func pushRecent(ids []string, id string) []string {
	out := []string{id}
	for _, x := range ids {
		if x != id && len(out) < recentMax {
			out = append(out, x)
		}
	}
	return out
}

func noteOpened(id string) {
	p, err := statePath("recent.json")
	if err != nil {
		return
	}
	raw, _ := json.Marshal(pushRecent(loadRecent(), id))
	_ = os.WriteFile(p, raw, 0o600)
}

// --- yana open ----------------------------------------------------------------

func openMain(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: yana open <path or id>")
	}
	s, err := loadSession()
	if err != nil {
		return err
	}
	if c, err := loadConfig(); err == nil {
		configuredEditor = c.Editor
	} else {
		fmt.Fprintln(os.Stderr, "yana:", err)
	}
	all, err := fetchTree(s)
	if err != nil {
		return err
	}
	note, ok := resolveNote(all, args[0])
	if !ok {
		return fmt.Errorf("no note at %s", args[0])
	}
	es, cmd, err := startEdit(s, note, nil)
	if err != nil {
		return err
	}
	noteOpened(note.ID)
	if es.readOnly {
		fmt.Fprintf(os.Stderr, "%s is read-only for you; changes will not be saved.\n", note.Path)
	}
	runErr := cmd.Run()
	saves, err := es.finish()
	if err != nil {
		return err
	}
	if runErr != nil {
		return fmt.Errorf("the editor: %w", runErr)
	}
	if saves > 0 {
		fmt.Fprintf(os.Stderr, "Saved %s.\n", note.Path)
	}
	return nil
}
