package server

import (
	"io/fs"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
)

// TreeNode is one directory or note in the sidebar tree.
type TreeNode struct {
	Type   string   `json:"type"` // "dir" | "note"
	Name   string   `json:"name"`
	Path   string   `json:"path"`
	ID     string   `json:"id,omitempty"`
	Title  string   `json:"title,omitempty"`
	Kind   string   `json:"kind,omitempty"`
	Order  *int     `json:"order,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	Public bool     `json:"public,omitempty"` // a public link is live
	// Conflict marks a copy parked beside this note by a collision,
	// nested under the note it belongs to rather than listed among its
	// siblings. ConflictOf names that note.
	Conflict   bool        `json:"conflict,omitempty"`
	ConflictOf string      `json:"conflict_of,omitempty"`
	Children   []*TreeNode `json:"children,omitempty"`
}

// SpaceTree is the tree of one space.
type SpaceTree struct {
	Name     string      `json:"name"`
	Notes    int         `json:"notes"`
	Children []*TreeNode `json:"children"`
}

// buildTree nests a flat, path-sorted note list into directories. Notes
// loose in the root land in a space named "". tags, keyed by note id,
// ride along on the note rows so the switcher can match on them. A
// conflict copy is held out of the directory and nested under the note
// it belongs to, so collisions sit beside their survivor instead of
// reading as ordinary siblings.
func buildTree(notes []index.Note, tags map[string][]string) []SpaceTree {
	type heldOut struct {
		node  *TreeNode
		dir   *TreeNode
		owner string
	}
	type spaceAcc struct {
		root      *TreeNode
		count     int
		conflicts []heldOut
	}
	spaces := map[string]*spaceAcc{}
	var order []string
	nodes := map[string]*TreeNode{} // note id → node, survivors only
	for _, n := range notes {
		acc, ok := spaces[n.Space]
		if !ok {
			acc = &spaceAcc{root: &TreeNode{Type: "dir", Name: n.Space, Path: n.Space}}
			spaces[n.Space] = acc
			order = append(order, n.Space)
		}
		acc.count++
		rel := n.RelPath
		if n.Space != "" {
			rel = strings.TrimPrefix(rel, n.Space+"/")
		}
		parts := strings.Split(rel, "/")
		cur := acc.root
		for i, part := range parts[:len(parts)-1] {
			dirPath := strings.Join(parts[:i+1], "/")
			if n.Space != "" {
				dirPath = n.Space + "/" + dirPath
			}
			cur, _ = childDir(cur, part, dirPath)
		}
		if n.ConflictOf != "" && n.ConflictOf != n.ID {
			acc.conflicts = append(acc.conflicts, heldOut{
				node: &TreeNode{
					Type: "note", Name: parts[len(parts)-1], Path: n.RelPath,
					ID: n.ID, Title: n.Title, Kind: n.Kind, Order: n.Order, Tags: tags[n.ID],
					Conflict: true, ConflictOf: n.ConflictOf,
				},
				dir:   cur,
				owner: n.ConflictOf,
			})
			continue
		}
		node := &TreeNode{
			Type: "note", Name: parts[len(parts)-1], Path: n.RelPath,
			ID: n.ID, Title: n.Title, Kind: n.Kind, Order: n.Order, Tags: tags[n.ID],
		}
		nodes[n.ID] = node
		cur.Children = append(cur.Children, node)
	}
	sort.Strings(order)
	out := make([]SpaceTree, 0, len(order))
	for _, name := range order {
		acc := spaces[name]
		sortTree(acc.root)
		for _, c := range acc.conflicts {
			if owner := nodes[c.owner]; owner != nil {
				owner.Children = append(owner.Children, c.node)
			} else {
				// The survivor is not in this listing; the copy reads
				// as the plain note it is.
				c.node.Conflict = false
				c.dir.Children = append(c.dir.Children, c.node)
				sortTree(acc.root)
			}
		}
		out = append(out, SpaceTree{Name: name, Notes: acc.count, Children: acc.root.Children})
	}
	return out
}

// markPublic sets Public on every note with a live public link, for the
// globe in the sidebar. A lookup failure marks nothing.
func (s *Server) markPublic(r *http.Request, tree []SpaceTree) {
	if s.Content == nil {
		return
	}
	ids, err := s.DB.PublicNoteIDs(r.Context(), time.Now())
	if err != nil || len(ids) == 0 {
		return
	}
	var walk func(nodes []*TreeNode)
	walk = func(nodes []*TreeNode) {
		for _, n := range nodes {
			if n.Type == "note" && ids[n.ID] {
				n.Public = true
			}
			walk(n.Children)
		}
	}
	for _, sp := range tree {
		walk(sp.Children)
	}
}

// withEmptyDirs adds the directories on disk that hold no notes yet (a
// folder just made, or one whose notes were all deleted) to the tree of
// their space, so a folder exists in the sidebar as soon as it exists
// on disk. Only spaces already in the tree are walked, which keeps the
// visibility rules where they are.
func (s *Server) withEmptyDirs(tree []SpaceTree) {
	for i := range tree {
		sp := &tree[i]
		if sp.Name == "" {
			continue
		}
		root := &TreeNode{Type: "dir", Name: sp.Name, Path: sp.Name, Children: sp.Children}
		base := filepath.Join(s.Root.Dir(), filepath.FromSlash(sp.Name))
		changed := false
		_ = filepath.WalkDir(base, func(abs string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() || abs == base {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "_assets" {
				return fs.SkipDir
			}
			rel, err := filepath.Rel(s.Root.Dir(), abs)
			if err != nil {
				return fs.SkipDir
			}
			rel = filepath.ToSlash(rel)
			if _, err := s.Root.Clean(rel); err != nil {
				return fs.SkipDir
			}
			parts := strings.Split(strings.TrimPrefix(rel, sp.Name+"/"), "/")
			cur := root
			for j, part := range parts {
				dirPath := sp.Name + "/" + strings.Join(parts[:j+1], "/")
				var made bool
				cur, made = childDir(cur, part, dirPath)
				changed = changed || made
			}
			return nil
		})
		if changed {
			sortTree(root)
		}
		sp.Children = root.Children
	}
}

// childDir returns the directory node under parent named name, making it
// when it is missing; made reports which.
func childDir(parent *TreeNode, name, path string) (d *TreeNode, made bool) {
	for _, c := range parent.Children {
		if c.Type == "dir" && c.Name == name {
			return c, false
		}
	}
	d = &TreeNode{Type: "dir", Name: name, Path: path}
	parent.Children = append(parent.Children, d)
	return d, true
}

// sortTree orders directories first (by name), then notes by explicit
// order, then title, case-insensitively.
func sortTree(n *TreeNode) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.Type != b.Type {
			return a.Type == "dir"
		}
		if a.Type == "dir" {
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		switch {
		case a.Order != nil && b.Order != nil && *a.Order != *b.Order:
			return *a.Order < *b.Order
		case a.Order != nil && b.Order == nil:
			return true
		case a.Order == nil && b.Order != nil:
			return false
		}
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	})
	for _, c := range n.Children {
		if c.Type == "dir" {
			sortTree(c)
		}
	}
}
