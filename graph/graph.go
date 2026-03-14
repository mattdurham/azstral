// Package graph provides the core node/edge representation for azstral.
// NOTE-001: The graph is the core abstraction for representing Go code structurally.
package graph

import (
	"fmt"
	"maps"
	"sort"
	"sync"
)

// NodeKind represents the type of a graph node.
// SPEC-006: Supported node types.
type NodeKind string

const (
	KindPackage   NodeKind = "package"
	KindFile      NodeKind = "file"
	KindFunction  NodeKind = "function"
	KindType      NodeKind = "type"
	KindVariable  NodeKind = "variable"
	KindComment   NodeKind = "comment"
	KindSpec      NodeKind = "spec"
	KindImport    NodeKind = "import"
	KindStatement NodeKind = "statement"

	// Expression-level node kinds for fine-grained AST representation.
	KindCall     NodeKind = "call"     // a call expression: fmt.Println(...)
	KindSelector NodeKind = "selector" // a selector: fmt.Println (pkg + method)
	KindIdent    NodeKind = "ident"    // an identifier: fmt, Println, x
	KindLiteral  NodeKind = "literal"  // a literal value: "Hello World!", 42, true
)

// EdgeKind represents the type of a graph edge.
// SPEC-007: Supported edge types.
type EdgeKind string

const (
	EdgeContains   EdgeKind = "contains"
	EdgeCalls      EdgeKind = "calls"
	EdgeReferences EdgeKind = "references"
	EdgeAnnotates  EdgeKind = "annotates"
	EdgeCovers     EdgeKind = "covers"
	EdgeCallee     EdgeKind = "callee"  // call → selector or ident being called
	EdgeArg        EdgeKind = "arg"     // call → argument node (ordered by metadata["pos"])
	EdgeReceiver   EdgeKind = "receiver" // selector → receiver ident (e.g. fmt)
	EdgeMethod     EdgeKind = "method"   // selector → method ident (e.g. Println)
)

// Node represents a single element in the code graph.
type Node struct {
	ID       string            `json:"id"`
	Kind     NodeKind          `json:"kind"`
	Name     string            `json:"name"`
	File     string            `json:"file,omitempty"`
	Line     int               `json:"line,omitempty"`
	EndLine  int               `json:"end_line,omitempty"`
	Text     string            `json:"text,omitempty"` // source text or rendered form
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Edge represents a directed relationship between two nodes.
type Edge struct {
	From string   `json:"from"`
	To   string   `json:"to"`
	Kind EdgeKind `json:"kind"`
}

// Graph holds all nodes and edges.
type Graph struct {
	mu    sync.RWMutex
	Nodes map[string]*Node `json:"nodes"`
	Edges []*Edge          `json:"edges"`
}

// New creates an empty graph.
func New() *Graph {
	return &Graph{
		Nodes: make(map[string]*Node),
	}
}

// AddNode adds a node to the graph. Returns error if ID already exists.
func (g *Graph) AddNode(n *Node) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.Nodes[n.ID]; exists {
		return fmt.Errorf("node %q already exists", n.ID)
	}
	g.Nodes[n.ID] = n
	return nil
}

// UpdateNode applies non-zero fields from the patch to an existing node.
// Only pointer fields that are non-nil are updated.
func (g *Graph) UpdateNode(id string, patch NodePatch) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.Nodes[id]
	if !ok {
		return fmt.Errorf("node %q not found", id)
	}
	if patch.Name != nil {
		n.Name = *patch.Name
	}
	if patch.Text != nil {
		n.Text = *patch.Text
	}
	if patch.File != nil {
		n.File = *patch.File
	}
	if patch.Line != nil {
		n.Line = *patch.Line
	}
	if patch.EndLine != nil {
		n.EndLine = *patch.EndLine
	}
	if patch.Metadata != nil {
		if n.Metadata == nil {
			n.Metadata = make(map[string]string)
		}
		maps.Copy(n.Metadata, patch.Metadata)
	}
	return nil
}

// NodePatch holds optional fields for updating a node.
type NodePatch struct {
	Name     *string
	Text     *string
	File     *string
	Line     *int
	EndLine  *int
	Metadata map[string]string
}

// GetNode returns a node by ID.
func (g *Graph) GetNode(id string) (*Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.Nodes[id]
	return n, ok
}

// AddEdge adds a directed edge between two nodes.
func (g *Graph) AddEdge(from, to string, kind EdgeKind) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.Nodes[from]; !ok {
		return fmt.Errorf("source node %q not found", from)
	}
	if _, ok := g.Nodes[to]; !ok {
		return fmt.Errorf("target node %q not found", to)
	}
	g.Edges = append(g.Edges, &Edge{From: from, To: to, Kind: kind})
	return nil
}

// NodesByKind returns all nodes of a given kind.
func (g *Graph) NodesByKind(kind NodeKind) []*Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var result []*Node
	for _, n := range g.Nodes {
		if n.Kind == kind {
			result = append(result, n)
		}
	}
	return result
}

// Children returns nodes connected by Contains edges from the given node,
// sorted by Line number (for deterministic code generation).
func (g *Graph) Children(parentID string) []*Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var result []*Node
	for _, e := range g.Edges {
		if e.From == parentID && e.Kind == EdgeContains {
			if n, ok := g.Nodes[e.To]; ok {
				result = append(result, n)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Line < result[j].Line
	})
	return result
}

// EdgesFrom returns all edges originating from a given node.
func (g *Graph) EdgesFrom(id string) []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var result []*Edge
	for _, e := range g.Edges {
		if e.From == id {
			result = append(result, e)
		}
	}
	return result
}

// EdgesTo returns all edges pointing to a given node.
func (g *Graph) EdgesTo(id string) []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var result []*Edge
	for _, e := range g.Edges {
		if e.To == id {
			result = append(result, e)
		}
	}
	return result
}
