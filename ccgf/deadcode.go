package ccgf

import (
	"sort"
	"strings"

	"github.com/matt/azstral/graph"
)

// DeadSymbol represents an unreferenced symbol.
type DeadSymbol struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	File string `json:"file"`
	Line int    `json:"line"`
}

// FindDeadCode finds defined symbols that are never referenced by other code.
// It walks the graph to build a set of "used" symbols (via calls, references,
// and callee edges) then reports the complement.
//
// Excluded from dead code:
//   - main(), init() functions
//   - Test*, Benchmark*, Example* functions
//   - Exported symbols in non-main packages (may be used externally)
//   - External/vendor nodes
func FindDeadCode(g *graph.Graph, includeExported bool) []DeadSymbol {
	// Collect all non-external defined symbols.
	type defn struct {
		node *graph.Node
		pkg  string // parent package name
	}
	var defs []defn

	for _, kind := range []graph.NodeKind{
		graph.KindFunction,
		graph.KindType,
		graph.KindVariable,
	} {
		for _, n := range g.NodesByKind(kind) {
			if n.Metadata["external"] == "true" {
				continue
			}
			if n.Metadata["raw"] == "true" {
				continue // grouped const/var blocks
			}
			defs = append(defs, defn{node: n})
		}
	}

	// Resolve parent package for each definition.
	for i := range defs {
		defs[i].pkg = packageOf(g, defs[i].node.ID)
	}

	// Build the set of referenced node IDs.
	used := make(map[string]bool)

	// Walk all edges to find references.
	for _, e := range g.Edges {
		switch e.Kind {
		case graph.EdgeCallee:
			// call → function: the function is used.
			used[e.To] = true
		case graph.EdgeReferences:
			used[e.To] = true
		case graph.EdgeCalls:
			used[e.To] = true
		}
	}

	// Also mark functions that are called indirectly through call nodes.
	// Walk function → contains → call → callee → target.
	for _, n := range g.NodesByKind(graph.KindCall) {
		for _, e := range g.EdgesFrom(n.ID) {
			if e.Kind == graph.EdgeCallee {
				used[e.To] = true
			}
		}
	}

	// Find dead symbols.
	var dead []DeadSymbol
	for _, d := range defs {
		n := d.node

		// Skip entry points.
		if isEntryPoint(n) {
			continue
		}

		// Skip exported symbols in non-main packages unless requested.
		if !includeExported && d.pkg != "main" && isExported(n.Name) {
			continue
		}

		if used[n.ID] {
			continue
		}

		dead = append(dead, DeadSymbol{
			ID:   n.ID,
			Kind: string(n.Kind),
			Name: n.Name,
			File: n.File,
			Line: n.Line,
		})
	}

	sort.Slice(dead, func(i, j int) bool {
		if dead[i].File != dead[j].File {
			return dead[i].File < dead[j].File
		}
		return dead[i].Line < dead[j].Line
	})

	return dead
}

func isEntryPoint(n *graph.Node) bool {
	if n.Kind != graph.KindFunction {
		return false
	}
	name := n.Name
	if name == "main" || name == "init" {
		return true
	}
	if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") || strings.HasPrefix(name, "Example") {
		return true
	}
	return false
}

func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	return name[0] >= 'A' && name[0] <= 'Z'
}

// packageOf walks contains edges upward to find the package name.
func packageOf(g *graph.Graph, nodeID string) string {
	for _, edge := range g.EdgesTo(nodeID) {
		if edge.Kind == graph.EdgeContains {
			parent, ok := g.GetNode(edge.From)
			if !ok {
				continue
			}
			if parent.Kind == graph.KindPackage {
				return parent.Name
			}
			if p := packageOf(g, parent.ID); p != "" {
				return p
			}
		}
	}
	return ""
}
