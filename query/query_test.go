package query

import (
	"testing"

	"github.com/matt/azstral/graph"
)

func buildQueryGraph() *graph.Graph {
	g := graph.New()

	g.AddNode(&graph.Node{ID: "pkg:main", Kind: graph.KindPackage, Name: "main"})
	g.AddNode(&graph.Node{ID: "file:main.go", Kind: graph.KindFile, Name: "main.go",
		File: "file:main.go", Line: 1})
	g.AddEdge("pkg:main", "file:main.go", graph.EdgeContains)

	g.AddNode(&graph.Node{ID: "func:ParseFile", Kind: graph.KindFunction, Name: "ParseFile",
		File: "file:main.go", Line: 10,
		Metadata: map[string]string{
			"params": "(g *Graph, path string)", "returns": "error",
			"cyclomatic": "8", "cognitive": "12",
		}})
	g.AddEdge("file:main.go", "func:ParseFile", graph.EdgeContains)

	g.AddNode(&graph.Node{ID: "func:readHeader", Kind: graph.KindFunction, Name: "readHeader",
		File: "file:main.go", Line: 50,
		Metadata: map[string]string{
			"params": "()", "returns": "Header",
			"cyclomatic": "3", "cognitive": "2",
		}})
	g.AddEdge("file:main.go", "func:readHeader", graph.EdgeContains)

	g.AddNode(&graph.Node{ID: "type:Header", Kind: graph.KindType, Name: "Header",
		File: "file:main.go", Line: 5, Text: "struct{ Version int }"})
	g.AddEdge("file:main.go", "type:Header", graph.EdgeContains)

	// ParseFile calls readHeader.
	g.AddNode(&graph.Node{ID: "call:0", Kind: graph.KindCall, Name: "readHeader()"})
	g.AddEdge("func:ParseFile", "call:0", graph.EdgeContains)
	g.AddEdge("call:0", "func:readHeader", graph.EdgeCallee)

	// External vendor function.
	g.AddNode(&graph.Node{ID: "func:fmt.Println", Kind: graph.KindFunction, Name: "Println",
		Metadata: map[string]string{"external": "true", "package": "fmt"}})
	g.AddNode(&graph.Node{ID: "call:1", Kind: graph.KindCall})
	g.AddEdge("func:ParseFile", "call:1", graph.EdgeContains)
	g.AddEdge("call:1", "func:fmt.Println", graph.EdgeCallee)

	return g
}

func TestNodeQuery_Kind(t *testing.T) {
	g := buildQueryGraph()
	nodes, err := NodeQuery(g, `kind == "function"`)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.Kind != graph.KindFunction {
			t.Errorf("got non-function: %s (%s)", n.ID, n.Kind)
		}
	}
	if len(nodes) < 2 {
		t.Errorf("expected at least 2 functions, got %d", len(nodes))
	}
}

func TestNodeQuery_Cyclomatic(t *testing.T) {
	g := buildQueryGraph()
	nodes, err := NodeQuery(g, `kind == "function" && cyclomatic > 5`)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "ParseFile" {
		t.Errorf("expected ParseFile (cyclo=8), got %v", nodes)
	}
}

func TestNodeQuery_NameContains(t *testing.T) {
	g := buildQueryGraph()
	nodes, err := NodeQuery(g, `name.contains("Header")`)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) < 1 {
		t.Errorf("expected nodes containing 'Header', got 0")
	}
}

func TestNodeQuery_External(t *testing.T) {
	g := buildQueryGraph()
	nodes, err := NodeQuery(g, `external == true`)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].ID != "func:fmt.Println" {
		t.Errorf("expected fmt.Println, got %v", nodes)
	}
}

func TestNodeQuery_CalleeIDs(t *testing.T) {
	g := buildQueryGraph()
	// ParseFile calls readHeader — find functions that call readHeader.
	nodes, err := NodeQuery(g, `kind == "function" && "func:readHeader" in callee_ids`)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "ParseFile" {
		t.Errorf("expected ParseFile as caller of readHeader, got %v", nodes)
	}
}

func TestNodeQuery_CallerIDs(t *testing.T) {
	g := buildQueryGraph()
	// readHeader is called by ParseFile.
	nodes, err := NodeQuery(g, `kind == "function" && "func:ParseFile" in caller_ids`)
	if err != nil {
		t.Fatal(err)
	}
	// Both readHeader and fmt.Println are called by ParseFile.
	if len(nodes) < 1 {
		t.Fatalf("expected nodes called by ParseFile, got 0")
	}
	names := make(map[string]bool)
	for _, n := range nodes {
		names[n.Name] = true
	}
	if !names["readHeader"] {
		t.Errorf("expected readHeader in results, got %v", nodes)
	}
}

func TestNodeQuery_ParentID(t *testing.T) {
	g := buildQueryGraph()
	nodes, err := NodeQuery(g, `parent_id == "pkg:main"`)
	if err != nil {
		t.Fatal(err)
	}
	// pkg:main directly contains file:main.go only.
	if len(nodes) != 1 {
		t.Errorf("expected 1 direct child of pkg:main, got %d", len(nodes))
	}
}

func TestNodeQuery_CalleeSizeFilter(t *testing.T) {
	g := buildQueryGraph()
	// ParseFile calls 2 things (readHeader + fmt.Println).
	nodes, err := NodeQuery(g, `kind == "function" && callee_ids.size() > 1`)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "ParseFile" {
		t.Errorf("expected ParseFile with 2 callees, got %v", nodes)
	}
}

func TestEdgeQuery_Kind(t *testing.T) {
	g := buildQueryGraph()
	edges, err := EdgeQuery(g, `kind == "callee"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) < 2 {
		t.Errorf("expected at least 2 callee edges, got %d", len(edges))
	}
}

func TestEdgeQuery_To(t *testing.T) {
	g := buildQueryGraph()
	edges, err := EdgeQuery(g, `to == "func:readHeader" && kind == "callee"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 callee edge to readHeader, got %d", len(edges))
	}
}

func TestNodeQuery_MetadataNum(t *testing.T) {
	g := buildQueryGraph()

	// Add bench metadata to ParseFile simulating run_bench output.
	g.UpdateNode("func:ParseFile", graph.NodePatch{
		Metadata: map[string]string{
			"bench_ns_op":     "1234.5",
			"bench_rows_op":   "42",
			"bench_allocs_op": "3",
		},
	})

	// Standard field query.
	nodes, err := NodeQuery(g, `kind == "function" && bench_ns_op > 1000.0`)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "ParseFile" {
		t.Errorf("bench_ns_op filter: expected ParseFile, got %v", nodes)
	}

	// Custom metric via metadata.num().
	nodes, err = NodeQuery(g, `metadata.num("bench_rows_op") > 20.0`)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "ParseFile" {
		t.Errorf("metadata.num filter: expected ParseFile, got %v", nodes)
	}

	// Combined: slow AND many rows.
	nodes, err = NodeQuery(g, `metadata.num("bench_rows_op") > 10.0 && bench_ns_op > 500.0`)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Errorf("combined filter: expected 1 result, got %d", len(nodes))
	}

	// Missing key returns 0.0, not an error.
	nodes, err = NodeQuery(g, `metadata.num("nonexistent") > 0.0`)
	if err != nil {
		t.Fatalf("missing key should not error: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("missing key should return 0, got %d nodes", len(nodes))
	}
}

func TestNodeQuery_CompileError(t *testing.T) {
	g := buildQueryGraph()
	_, err := NodeQuery(g, `this is not valid cel`)
	if err == nil {
		t.Error("expected compile error for invalid CEL expression")
	}
}
