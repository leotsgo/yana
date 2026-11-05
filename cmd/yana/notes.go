// The notes side of the command line's client: the tree, a note's text
// and its saves, and the recently opened list.
package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
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
	// Aliases are the names the note claims in its frontmatter, which
	// wikilinks resolve by too.
	Aliases []string `json:"aliases,omitempty"`
}

type treeNode struct {
	Type     string     `json:"type"`
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Kind     string     `json:"kind"`
	Tags     []string   `json:"tags"`
	Aliases  []string   `json:"aliases"`
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
				out = append(out, entry{Path: n.Path, Name: n.Name, ID: n.ID, Title: n.Title, Kind: n.Kind, Tags: n.Tags, Aliases: n.Aliases})
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

// findNotes is what a name typed at the shell means: the note at that
// id or path, else every note titled so, ignoring case. One result opens;
// several are for the picker to choose from.
func findNotes(all []entry, arg string) []entry {
	if e, ok := resolveNote(all, arg); ok {
		return []entry{e}
	}
	var out []entry
	for _, e := range all {
		if !e.Dir && e.Title != "" && strings.EqualFold(e.Title, strings.TrimSpace(arg)) {
			out = append(out, e)
		}
	}
	return out
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
