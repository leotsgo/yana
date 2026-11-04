package index

import (
	"context"
	"sort"
	"strings"
)

// GraphNote is one node of the graph view.
type GraphNote struct {
	ID    string `json:"id"`
	Space string `json:"space"`
	Path  string `json:"path"`
	Title string `json:"title"`
}

// GraphGhost is a link target no note answers to, with the notes that
// point at it.
type GraphGhost struct {
	Space  string `json:"space"`
	Target string `json:"target"` // raw target as first seen
	From   []int  `json:"from"`   // indices into Graph.Notes, ascending
}

// GraphTag is a tag within one space and the notes carrying it.
type GraphTag struct {
	Space string `json:"space"`
	Tag   string `json:"tag"`
	Notes []int  `json:"notes"` // indices into Graph.Notes, ascending
}

// Graph is the link structure of the notes a caller may see. Every
// reference to a note is an index into Notes.
type Graph struct {
	Notes  []GraphNote  `json:"notes"`  // ordered by path
	Links  [][2]int     `json:"links"`  // undirected, i < j, sorted, deduplicated
	Ghosts []GraphGhost `json:"ghosts"` // ordered by (space, lower(target))
	Tags   []GraphTag   `json:"tags"`   // ordered by (space, tag)
}

// Graph builds the graph of the given spaces; nil is every space, an
// empty list none. Resolved links and embeds become edges; only
// unresolved links (not embeds) become ghosts.
func (db *DB) Graph(ctx context.Context, allowed []string) (Graph, error) {
	g := Graph{Notes: []GraphNote{}, Links: [][2]int{}, Ghosts: []GraphGhost{}, Tags: []GraphTag{}}
	q := `SELECT id, space, rel_path, title FROM notes`
	var args []any
	if allowed != nil {
		if len(allowed) == 0 {
			return g, nil
		}
		ph := make([]string, len(allowed))
		for i, sp := range allowed {
			ph[i] = "?"
			args = append(args, sp)
		}
		q += ` WHERE space IN (` + strings.Join(ph, ",") + `)`
	}
	rows, err := db.readers.QueryContext(ctx, q+` ORDER BY rel_path`, args...)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	idx := map[string]int{}
	for rows.Next() {
		var n GraphNote
		if err := rows.Scan(&n.ID, &n.Space, &n.Path, &n.Title); err != nil {
			return g, err
		}
		idx[n.ID] = len(g.Notes)
		g.Notes = append(g.Notes, n)
	}
	if err := rows.Err(); err != nil {
		return g, err
	}

	// Edges: both ends must be among the notes, so spaces filter themselves.
	lrows, err := db.readers.QueryContext(ctx, `SELECT from_id, to_id FROM links WHERE resolved = 1`)
	if err != nil {
		return g, err
	}
	defer lrows.Close()
	seen := map[[2]int]bool{}
	for lrows.Next() {
		var from, to string
		if err := lrows.Scan(&from, &to); err != nil {
			return g, err
		}
		i, ok1 := idx[from]
		j, ok2 := idx[to]
		if !ok1 || !ok2 || i == j {
			continue
		}
		if i > j {
			i, j = j, i
		}
		if p := [2]int{i, j}; !seen[p] {
			seen[p] = true
			g.Links = append(g.Links, p)
		}
	}
	if err := lrows.Err(); err != nil {
		return g, err
	}
	sort.Slice(g.Links, func(a, b int) bool {
		if g.Links[a][0] != g.Links[b][0] {
			return g.Links[a][0] < g.Links[b][0]
		}
		return g.Links[a][1] < g.Links[b][1]
	})

	// Ghosts: walking notes in path order makes "first seen" and the
	// ascending From lists fall out of the query order.
	grows, err := db.readers.QueryContext(ctx, `SELECT l.from_id, l.raw_target
		FROM links l JOIN notes n ON n.id = l.from_id
		WHERE l.resolved = 0 AND l.kind = 'link' ORDER BY n.rel_path, l.raw_target`)
	if err != nil {
		return g, err
	}
	defer grows.Close()
	ghosts := map[[2]string]*GraphGhost{}
	for grows.Next() {
		var from, target string
		if err := grows.Scan(&from, &target); err != nil {
			return g, err
		}
		i, ok := idx[from]
		if !ok {
			continue
		}
		space := g.Notes[i].Space
		key := [2]string{space, strings.ToLower(target)}
		gh := ghosts[key]
		if gh == nil {
			gh = &GraphGhost{Space: space, Target: target, From: []int{}}
			ghosts[key] = gh
		}
		if n := len(gh.From); n == 0 || gh.From[n-1] != i {
			gh.From = append(gh.From, i)
		}
	}
	if err := grows.Err(); err != nil {
		return g, err
	}
	for _, gh := range ghosts {
		g.Ghosts = append(g.Ghosts, *gh)
	}
	sort.Slice(g.Ghosts, func(a, b int) bool {
		x, y := g.Ghosts[a], g.Ghosts[b]
		if x.Space != y.Space {
			return x.Space < y.Space
		}
		return strings.ToLower(x.Target) < strings.ToLower(y.Target)
	})

	trows, err := db.readers.QueryContext(ctx, `SELECT t.note_id, t.tag
		FROM tags t JOIN notes n ON n.id = t.note_id ORDER BY n.space, t.tag, n.rel_path`)
	if err != nil {
		return g, err
	}
	defer trows.Close()
	for trows.Next() {
		var id, tag string
		if err := trows.Scan(&id, &tag); err != nil {
			return g, err
		}
		i, ok := idx[id]
		if !ok {
			continue
		}
		space := g.Notes[i].Space
		if n := len(g.Tags); n > 0 && g.Tags[n-1].Space == space && g.Tags[n-1].Tag == tag {
			g.Tags[n-1].Notes = append(g.Tags[n-1].Notes, i)
		} else {
			g.Tags = append(g.Tags, GraphTag{Space: space, Tag: tag, Notes: []int{i}})
		}
	}
	return g, trows.Err()
}
