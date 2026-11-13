package flowchart

import "sort"

// Box is a placed rectangle.
type Box struct{ X, Y, W, H float64 }

const (
	rankGap  = 80.0
	nodeGap  = 50.0
	sgPad    = 30.0
	sgTitleH = 36.0
)

// nodeSize is the box size of a node from its label and shape.
func nodeSize(n Node) (w, h float64) {
	label := n.ID
	if n.Label != nil {
		label = *n.Label
	}
	tw, th := TextSize(label, 20)
	w, h = max(tw+40, 120), max(th+30, 60)
	switch n.Shape {
	case "diamond", "hexagon": // hexagon is drawn as a diamond
		w, h = w*1.5, h*1.5
	case "circle", "double-circle":
		w = max(w, h) * 1.2
		h = w
	}
	return w, h
}

// Layout positions every node and subgraph. Nested subgraph boxes contain their members.
func Layout(g Graph) (nodes map[string]Box, subgraphs map[string]Box) {
	l := &layouter{g: g, parent: map[string]string{}, size: map[string][2]float64{}, rel: map[string]map[string]Box{}}
	for _, n := range g.Nodes {
		l.parent["n:"+n.ID] = n.Subgraph
	}
	for _, s := range g.Subgraphs {
		l.parent["s:"+s.ID] = s.Parent
	}
	l.unit("")
	nodes, subgraphs = map[string]Box{}, map[string]Box{}
	l.place("", 0, 0, nodes, subgraphs)
	return nodes, subgraphs
}

type layouter struct {
	g      Graph
	parent map[string]string // member key ("n:id" / "s:id") -> containing subgraph id
	size   map[string][2]float64
	rel    map[string]map[string]Box // unit id -> member key -> box relative to the unit's content origin
}

// lift maps a node to the member of unit that contains it, or "".
func (l *layouter) lift(nodeID, unit string) string {
	k := "n:" + nodeID
	if _, ok := l.parent[k]; !ok {
		return ""
	}
	for {
		p := l.parent[k]
		if p == unit {
			return k
		}
		if p == "" {
			return ""
		}
		k = "s:" + p
	}
}

// unit lays out the members of subgraph id ("" = top level) and records the
// size of its content; a subgraph's own size is content plus padding and title.
func (l *layouter) unit(id string) (cw, ch float64) {
	var keys []string
	for _, n := range l.g.Nodes {
		if n.Subgraph == id {
			keys = append(keys, "n:"+n.ID)
			w, h := nodeSize(n)
			l.size["n:"+n.ID] = [2]float64{w, h}
		}
	}
	for _, s := range l.g.Subgraphs {
		if s.Parent == id {
			keys = append(keys, "s:"+s.ID)
			w, h := l.unit(s.ID)
			w, h = w+2*sgPad, h+2*sgPad+sgTitleH
			titleW, _ := TextSize(s.Title, 20)
			w = max(w, titleW+2*sgPad)
			l.size["s:"+s.ID] = [2]float64{w, h}
		}
	}
	idx := map[string]int{}
	for i, k := range keys {
		idx[k] = i
	}
	horiz := l.g.Dir == "LR" || l.g.Dir == "RL"
	tw := make([]float64, len(keys)) // size across the rank, in top-to-bottom space
	th := make([]float64, len(keys)) // size along the rank axis
	for i, k := range keys {
		w, h := l.size[k][0], l.size[k][1]
		if horiz {
			w, h = h, w
		}
		tw[i], th[i] = w, h
	}

	// Edges lifted to this unit's members; self-loops dropped, duplicates merged.
	type pair struct{ a, b int }
	seen := map[pair]bool{}
	var edges []pair
	for _, e := range l.g.Edges {
		a, aok := idx[l.lift(e.From, id)]
		b, bok := idx[l.lift(e.To, id)]
		if !aok || !bok || a == b || seen[pair{a, b}] {
			continue
		}
		seen[pair{a, b}] = true
		edges = append(edges, pair{a, b})
	}

	// DFS finds back edges; they are reversed so the graph is acyclic.
	n := len(keys)
	out := make([][]int, n)
	for _, e := range edges {
		out[e.a] = append(out[e.a], e.b)
	}
	state := make([]int, n) // 0 new, 1 on stack, 2 done
	back := map[pair]bool{}
	var dfs func(u int)
	dfs = func(u int) {
		state[u] = 1
		for _, v := range out[u] {
			switch state[v] {
			case 0:
				dfs(v)
			case 1:
				back[pair{u, v}] = true
			}
		}
		state[u] = 2
	}
	for u := 0; u < n; u++ {
		if state[u] == 0 {
			dfs(u)
		}
	}
	var dag []pair
	indeg := make([]int, n)
	succ := make([][]int, n)
	for _, e := range edges {
		if back[e] {
			e = pair{e.b, e.a}
		}
		dag = append(dag, e)
		succ[e.a] = append(succ[e.a], e.b)
		indeg[e.b]++
	}

	// Longest-path ranks.
	rank := make([]int, n)
	var queue []int
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, v := range succ[u] {
			rank[v] = max(rank[v], rank[u]+1)
			if indeg[v]--; indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	nr := 0
	for _, r := range rank {
		nr = max(nr, r+1)
	}
	ranks := make([][]int, nr)
	for i := 0; i < n; i++ {
		ranks[rank[i]] = append(ranks[rank[i]], i)
	}

	// Barycenter ordering, 4 alternating sweeps. ponytail: only neighbours in
	// the adjacent rank count and long edges get no dummy nodes, so crossings
	// are not minimal; add dummy nodes if layouts look tangled.
	pos := make([]float64, n)
	setPos := func() {
		for _, r := range ranks {
			for p, v := range r {
				pos[v] = float64(p)
			}
		}
	}
	setPos()
	for s := 0; s < 4; s++ {
		order := make([]int, 0, nr)
		for r := 0; r < nr; r++ {
			order = append(order, r)
		}
		down := s%2 == 0
		if !down {
			for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
				order[i], order[j] = order[j], order[i]
			}
		}
		for _, r := range order {
			from := r - 1
			if !down {
				from = r + 1
			}
			if from < 0 || from >= nr {
				continue
			}
			bary := map[int]float64{}
			for _, v := range ranks[r] {
				sum, cnt := 0.0, 0.0
				for _, e := range dag {
					var other int
					switch {
					case down && e.b == v:
						other = e.a
					case !down && e.a == v:
						other = e.b
					default:
						continue
					}
					if rank[other] == from {
						sum += pos[other]
						cnt++
					}
				}
				if cnt == 0 {
					bary[v] = pos[v]
				} else {
					bary[v] = sum / cnt
				}
			}
			sort.SliceStable(ranks[r], func(i, j int) bool { return bary[ranks[r][i]] < bary[ranks[r][j]] })
			setPos()
		}
	}

	// Place ranks top to bottom, each rank centred.
	rw := make([]float64, nr)
	rh := make([]float64, nr)
	for r, vs := range ranks {
		for i, v := range vs {
			rw[r] += tw[v]
			if i > 0 {
				rw[r] += nodeGap
			}
			rh[r] = max(rh[r], th[v])
		}
		cw = max(cw, rw[r])
	}
	tx, ty := make([]float64, n), make([]float64, n)
	y := 0.0
	for r, vs := range ranks {
		x := (cw - rw[r]) / 2
		for _, v := range vs {
			tx[v], ty[v] = x, y+(rh[r]-th[v])/2
			x += tw[v] + nodeGap
		}
		y += rh[r] + rankGap
	}
	TH := max(y-rankGap, 0)

	// Map top-to-bottom space to the real direction.
	rel := map[string]Box{}
	for i, k := range keys {
		w, h := l.size[k][0], l.size[k][1]
		var b Box
		switch l.g.Dir {
		case "LR":
			b = Box{ty[i], tx[i], w, h}
		case "RL":
			b = Box{TH - ty[i] - th[i], tx[i], w, h}
		case "BT":
			b = Box{tx[i], TH - ty[i] - th[i], w, h}
		default:
			b = Box{tx[i], ty[i], w, h}
		}
		rel[k] = b
	}
	l.rel[id] = rel
	if horiz {
		return TH, cw
	}
	return cw, TH
}

func (l *layouter) place(unit string, ox, oy float64, nodes, sgs map[string]Box) {
	for k, b := range l.rel[unit] {
		b.X += ox
		b.Y += oy
		if k[0] == 'n' {
			nodes[k[2:]] = b
		} else {
			sgs[k[2:]] = b
			l.place(k[2:], b.X+sgPad, b.Y+sgPad+sgTitleH, nodes, sgs)
		}
	}
}
