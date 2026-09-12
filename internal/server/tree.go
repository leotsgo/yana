package server

import (
	"sort"
	"strings"

	"github.com/madeofpendletonwool/yana/internal/index"
)

// TreeNode is one directory or note in the sidebar tree.
type TreeNode struct {
	Type     string      `json:"type"` // "dir" | "note"
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	ID       string      `json:"id,omitempty"`
	Title    string      `json:"title,omitempty"`
	Kind     string      `json:"kind,omitempty"`
	Order    *int        `json:"order,omitempty"`
	Children []*TreeNode `json:"children,omitempty"`
}

// SpaceTree is the tree of one space.
type SpaceTree struct {
	Name     string      `json:"name"`
	Notes    int         `json:"notes"`
	Children []*TreeNode `json:"children"`
}

// buildTree nests a flat, path-sorted note list into directories. Notes
// loose in the root land in a space named "".
func buildTree(notes []index.Note) []SpaceTree {
	type spaceAcc struct {
		root  *TreeNode
		count int
	}
	spaces := map[string]*spaceAcc{}
	var order []string
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
			cur = childDir(cur, part, dirPath)
		}
		cur.Children = append(cur.Children, &TreeNode{
			Type: "note", Name: parts[len(parts)-1], Path: n.RelPath,
			ID: n.ID, Title: n.Title, Kind: n.Kind, Order: n.Order,
		})
	}
	sort.Strings(order)
	out := make([]SpaceTree, 0, len(order))
	for _, name := range order {
		acc := spaces[name]
		sortTree(acc.root)
		out = append(out, SpaceTree{Name: name, Notes: acc.count, Children: acc.root.Children})
	}
	return out
}

func childDir(parent *TreeNode, name, path string) *TreeNode {
	for _, c := range parent.Children {
		if c.Type == "dir" && c.Name == name {
			return c
		}
	}
	d := &TreeNode{Type: "dir", Name: name, Path: path}
	parent.Children = append(parent.Children, d)
	return d
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
