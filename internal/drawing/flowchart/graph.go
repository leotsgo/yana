// Package flowchart parses Mermaid flowcharts into a Graph.
package flowchart

import (
	"errors"
	"fmt"
)

// Graph is a parsed flowchart.
type Graph struct {
	Dir       string     // "TB", "BT", "LR", "RL" ("TD" normalised to "TB"; default "TB")
	Nodes     []Node     // first-mention order
	Edges     []Edge     // source order
	Subgraphs []Subgraph // opening order
}

// Node is a flowchart node. Label nil and Shape "" mean "never given".
type Node struct {
	ID          string
	Label       *string
	Shape       string  // rect, round, diamond, circle, double-circle, stadium, cylinder, subroutine, hexagon, parallelogram, parallelogram-alt, trapezoid, trapezoid-alt, asymmetric
	Stroke      string  // "" not given; #rgb expanded to #rrggbb
	Fill        string  // same
	StrokeWidth float64 // 0 not given
	Subgraph    string  // innermost subgraph whose body first mentions it; "" top level
}

// Edge connects two nodes.
type Edge struct {
	From, To string
	Label    string
	Line     string // "solid", "dotted", "thick"
	Arrow    bool   // false for ---, -.-, ===
}

// Subgraph is a named group of nodes.
type Subgraph struct{ ID, Title, Parent string }

// ErrNotFlowchart means the first statement is not "flowchart" or "graph".
var ErrNotFlowchart = errors.New("not a flowchart: expected a first line like \"flowchart TB\"")

// ParseError is a syntax error at a 1-based source line.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }
