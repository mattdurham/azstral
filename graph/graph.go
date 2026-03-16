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

	// Statement-level node kinds — children of functions and other statements.
	KindFor    NodeKind = "for"    // for loop or range loop
	KindIf     NodeKind = "if"     // if / else-if / else
	KindSwitch NodeKind = "switch" // switch or type switch
	KindSelect NodeKind = "select" // select statement
	KindReturn NodeKind = "return" // return statement
	KindDefer  NodeKind = "defer"  // defer statement
	KindGo     NodeKind = "go"     // go statement (goroutine)
	KindAssign NodeKind = "assign" // assignment or short variable declaration
	KindSend   NodeKind = "send"   // channel send (<-)
	KindBranch NodeKind = "branch" // break / continue / goto / fallthrough
	KindLocal  NodeKind = "local"  // local variable, parameter, or named return (from go/types)

	// Sub-kinds for finer-grained statement classification.
	// These replace the parent kind + metadata["range"]/"op"/"tok" pattern,
	// enabling direct CEL queries: kind == "for.range", kind == "assign.decl".
	KindForRange    NodeKind = "for.range"    // for k, v := range x      name=x
	KindForCond     NodeKind = "for.cond"     // for cond { }             name=cond
	KindForLoop     NodeKind = "for.loop"     // for init; cond; post { } name=cond
	KindForBare     NodeKind = "for.bare"     // for { }                  name=""
	KindAssignDecl  NodeKind = "assign.decl"  // x := expr                name=lhs
	KindAssignSet   NodeKind = "assign.set"   // x = expr                 name=lhs
	KindAssignInc   NodeKind = "assign.inc"   // x++                      name=x
	KindAssignDec   NodeKind = "assign.dec"   // x--                      name=x
	KindAssignOp    NodeKind = "assign.op"    // x += expr                name=lhs
	KindBranchBreak    NodeKind = "branch.break"       // break [label]
	KindBranchContinue NodeKind = "branch.continue"    // continue [label]
	KindBranchGoto     NodeKind = "branch.goto"        // goto label
	KindBranchFall     NodeKind = "branch.fallthrough" // fallthrough

	// Expression-level node kinds for fine-grained AST representation.
	KindCall     NodeKind = "call"     // a call expression: fmt.Println(...)
	KindSelector NodeKind = "selector" // a selector: fmt.Println (pkg + method)
	KindIdent    NodeKind = "ident"    // an identifier: fmt, Println, x
	KindLiteral  NodeKind = "literal"  // a literal value: "Hello World!", 42, true

	// Expression-tree node kinds — fine-grained AST nodes for expression analysis.
	// SPEC-008: Expression node kinds.
	// NOTE: Any changes to this file must be reflected in SPECS.md and NOTES.md.
	KindExprBinary     NodeKind = "expr:binary"     // binary op: a + b, x == y
	KindExprUnary      NodeKind = "expr:unary"       // unary op: -x, !ok, ^n
	KindExprIdent      NodeKind = "expr:ident"       // identifier leaf: x, items
	KindExprSelector   NodeKind = "expr:selector"    // selector: os.Stderr, s.Field
	KindExprIndex      NodeKind = "expr:index"       // index: items[0], m[k]
	KindExprLiteral    NodeKind = "expr:literal"     // basic literal: 42, "hello", true
	KindExprComposite  NodeKind = "expr:composite"   // composite literal: Point{1, 2}
	KindExprTypeAssert NodeKind = "expr:typeassert"  // type assertion: v.(int)
	KindExprFunc       NodeKind = "expr:func"        // function literal: func(x int) bool { ... }
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
	mu      sync.RWMutex
	Nodes   map[string]*Node `json:"nodes"`
	Edges   []*Edge          `json:"edges"`
	fileIdx int
	fileMap    map[string]string // full fileID → short "f{n}"
	fileRevMap map[string]string // short "f{n}" → full fileID
	nodeIdx    int
	nodeToIdx  map[string]int // nodeID → integer
	idxToNode  []string       // integer → nodeID (index 0 unused; idx starts at 1)
}

// New creates an empty graph.
func New() *Graph {
	return &Graph{
		Nodes:      make(map[string]*Node),
		fileMap:    make(map[string]string),
		fileRevMap: make(map[string]string),
		nodeToIdx:  make(map[string]int),
		idxToNode:  []string{""},  // slot 0 unused; valid indices start at 1
	}
}

// FileShort returns the short index ("f0", "f1", ...) for a file ID,
// registering it if this is the first time it is seen.
// Thread-safe.
func (g *Graph) FileShort(fileID string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s, ok := g.fileMap[fileID]; ok {
		return s
	}
	s := fmt.Sprintf("f%d", g.fileIdx)
	g.fileIdx++
	g.fileMap[fileID] = s
	g.fileRevMap[s] = fileID
	return s
}

// FileFull resolves a short file index back to the full file ID.
func (g *Graph) FileFull(short string) string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.fileRevMap[short]
}

// FileMap returns a snapshot of the short→full mapping for serialisation.
func (g *Graph) FileMapping() map[string]string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make(map[string]string, len(g.fileRevMap))
	for k, v := range g.fileRevMap {
		out[k] = v
	}
	return out
}

// AddNode adds a node to the graph. Returns error if ID already exists.
// Each new node is assigned a globally unique integer index (starting at 1).
func (g *Graph) AddNode(n *Node) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.Nodes[n.ID]; exists {
		return fmt.Errorf("node %q already exists", n.ID)
	}
	g.Nodes[n.ID] = n
	g.nodeIdx++
	g.nodeToIdx[n.ID] = g.nodeIdx
	g.idxToNode = append(g.idxToNode, n.ID)
	return nil
}

// NodeIdx returns the integer index for a node ID, or 0 if not found.
func (g *Graph) NodeIdx(id string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.nodeToIdx[id]
}

// NodeByIdx returns the node for a given integer index, or nil.
func (g *Graph) NodeByIdx(idx int) *Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if idx < 1 || idx >= len(g.idxToNode) {
		return nil
	}
	return g.Nodes[g.idxToNode[idx]]
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

// Reset clears all nodes, edges, and all registries.
func (g *Graph) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Nodes = make(map[string]*Node)
	g.Edges = nil
	g.fileIdx = 0
	g.fileMap = make(map[string]string)
	g.fileRevMap = make(map[string]string)
	g.nodeIdx = 0
	g.nodeToIdx = make(map[string]int)
	g.idxToNode = []string{""}
}

// RenameNode changes a node's ID and name, and updates all edges that reference
// the old ID. Returns an error if the old ID doesn't exist or the new ID is
// already taken.
func (g *Graph) RenameNode(oldID, newID, newName string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.Nodes[oldID]
	if !ok {
		return fmt.Errorf("node %q not found", oldID)
	}
	if _, exists := g.Nodes[newID]; exists {
		return fmt.Errorf("node %q already exists", newID)
	}
	// Move to new ID.
	n.ID = newID
	if newName != "" {
		n.Name = newName
	}
	g.Nodes[newID] = n
	delete(g.Nodes, oldID)
	// Update all edges that reference the old ID.
	for _, e := range g.Edges {
		if e.From == oldID {
			e.From = newID
		}
		if e.To == oldID {
			e.To = newID
		}
	}
	return nil
}

// RemoveEdge removes a directed edge. Returns error if no matching edge exists.
func (g *Graph) RemoveEdge(from, to string, kind EdgeKind) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Kind == kind {
			continue // drop this edge
		}
		g.Edges[n] = e
		n++
	}
	removed := len(g.Edges) - n
	g.Edges = g.Edges[:n]
	if removed == 0 {
		return fmt.Errorf("edge %s -[%s]-> %s not found", from, kind, to)
	}
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

func (g *Graph) DeleteNode(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.Nodes[id]; !ok {
		return fmt.Errorf("node %q not found", id)
	}
	delete(g.Nodes, id)
	// Remove all edges involving this node.
	n := 0
	for _, e := range g.Edges {
		if e.From == id || e.To == id {
			continue
		}
		g.Edges[n] = e
		n++
	}
	g.Edges = g.Edges[:n]
	return nil
}

func (g *Graph) DeleteNodes(ids []string) (int, []string) {
	var errs []string
	deleted := 0
	for _, id := range ids {
		if err := g.DeleteNode(id); err != nil {
			errs = append(errs, err.Error())
		} else {
			deleted++
		}
	}
	return deleted, errs
}
