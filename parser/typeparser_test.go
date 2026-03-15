package parser

import (
	"testing"

	"github.com/matt/azstral/graph"
)

// TestLoadPackages_Self type-checks azstral itself and verifies qualified_id is set.
func TestLoadPackages_Self(t *testing.T) {
	g := graph.New()

	// Parse azstral's graph package with type info.
	n, err := LoadPackages(g, "../graph")
	if err != nil {
		t.Logf("load warning: %v", err)
	}
	if n == 0 {
		t.Fatal("no files loaded")
	}

	// Every function node in graph/ should have a qualified_id.
	missing := 0
	for _, node := range g.NodesByKind(graph.KindFunction) {
		if node.Metadata["external"] == "true" {
			continue
		}
		if node.Metadata["qualified_id"] == "" {
			missing++
			t.Logf("missing qualified_id: %s", node.ID)
		}
	}
	if missing > 0 {
		t.Errorf("%d function nodes missing qualified_id", missing)
	}

	// AddNode should have qualified_id containing the package path.
	addNode, ok := g.GetNode("func:*Graph.AddNode")
	if !ok {
		// Try alternate ID format.
		for _, fn := range g.NodesByKind(graph.KindFunction) {
			if fn.Name == "AddNode" {
				addNode = fn
				ok = true
				break
			}
		}
	}
	if !ok {
		t.Fatal("AddNode function not found")
	}
	qid := addNode.Metadata["qualified_id"]
	if qid == "" {
		t.Error("AddNode has no qualified_id")
	}
	t.Logf("AddNode qualified_id: %s", qid)
	if !contains(qid, "azstral") {
		t.Errorf("qualified_id %q does not contain 'azstral'", qid)
	}
}

func TestLoadPackages_VarDictionary(t *testing.T) {
	g := graph.New()
	_, err := LoadPackages(g, "../graph")
	if err != nil {
		t.Logf("warning: %v", err)
	}

	locals := g.NodesByKind(graph.KindLocal)
	if len(locals) == 0 {
		t.Fatal("no local variable nodes created")
	}
	t.Logf("created %d local variable nodes", len(locals))

	// Show a sample of what was captured.
	shown := 0
	for _, n := range locals {
		if shown >= 10 {
			break
		}
		t.Logf("  %s  type=%s  scope=%s  file=%s:%d",
			n.ID, n.Metadata["type"], n.Metadata["scope"],
			n.Name, n.Line)
		shown++
	}

	// Check that reference edges exist.
	refCount := 0
	for _, e := range g.Edges {
		if e.Kind == graph.EdgeReferences {
			if _, ok := g.GetNode(e.To); ok {
				if to, _ := g.GetNode(e.To); to != nil && to.Kind == graph.KindLocal {
					refCount++
				}
			}
		}
	}
	t.Logf("reference edges to local vars: %d", refCount)
	if refCount == 0 {
		t.Error("no reference edges to local variables found")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
