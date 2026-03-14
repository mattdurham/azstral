package ccgf

import (
	"testing"

	"github.com/matt/azstral/graph"
)

func TestFindDeadCode_Basic(t *testing.T) {
	g := buildTestGraph()

	dead := FindDeadCode(g, true)

	// readHeader is called by ParseFile → not dead.
	for _, d := range dead {
		if d.Name == "readHeader" {
			t.Errorf("readHeader should not be dead — it's called by ParseFile")
		}
	}

	t.Logf("dead symbols: %v", dead)
}

func TestFindDeadCode_UnusedFunction(t *testing.T) {
	g := buildTestGraph()

	// Add an unused function.
	g.AddNode(&graph.Node{
		ID: "func:unused", Kind: graph.KindFunction, Name: "unused",
		File: "main.go", Line: 20,
	})
	g.AddEdge("file:main.go", "func:unused", graph.EdgeContains)

	dead := FindDeadCode(g, true)

	found := false
	for _, d := range dead {
		if d.Name == "unused" {
			found = true
		}
	}
	if !found {
		t.Errorf("unused function should be dead")
	}
}

func TestFindDeadCode_ExcludesMain(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "pkg:main", Kind: graph.KindPackage, Name: "main"})
	g.AddNode(&graph.Node{ID: "file:main.go", Kind: graph.KindFile, Name: "main.go"})
	g.AddEdge("pkg:main", "file:main.go", graph.EdgeContains)
	g.AddNode(&graph.Node{ID: "func:main", Kind: graph.KindFunction, Name: "main"})
	g.AddEdge("file:main.go", "func:main", graph.EdgeContains)
	g.AddNode(&graph.Node{ID: "func:init", Kind: graph.KindFunction, Name: "init"})
	g.AddEdge("file:main.go", "func:init", graph.EdgeContains)

	dead := FindDeadCode(g, true)

	for _, d := range dead {
		if d.Name == "main" || d.Name == "init" {
			t.Errorf("%s should not be reported as dead", d.Name)
		}
	}
}

func TestFindDeadCode_ExcludesTestFuncs(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "pkg:foo_test", Kind: graph.KindPackage, Name: "foo_test"})
	g.AddNode(&graph.Node{ID: "file:foo_test.go", Kind: graph.KindFile, Name: "foo_test.go"})
	g.AddEdge("pkg:foo_test", "file:foo_test.go", graph.EdgeContains)
	g.AddNode(&graph.Node{ID: "func:TestFoo", Kind: graph.KindFunction, Name: "TestFoo"})
	g.AddEdge("file:foo_test.go", "func:TestFoo", graph.EdgeContains)
	g.AddNode(&graph.Node{ID: "func:BenchmarkFoo", Kind: graph.KindFunction, Name: "BenchmarkFoo"})
	g.AddEdge("file:foo_test.go", "func:BenchmarkFoo", graph.EdgeContains)

	dead := FindDeadCode(g, true)

	for _, d := range dead {
		if d.Name == "TestFoo" || d.Name == "BenchmarkFoo" {
			t.Errorf("%s should not be reported as dead", d.Name)
		}
	}
}

func TestFindDeadCode_ExportedInLibrary(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "pkg:mylib", Kind: graph.KindPackage, Name: "mylib"})
	g.AddNode(&graph.Node{ID: "file:lib.go", Kind: graph.KindFile, Name: "lib.go"})
	g.AddEdge("pkg:mylib", "file:lib.go", graph.EdgeContains)
	g.AddNode(&graph.Node{ID: "func:DoStuff", Kind: graph.KindFunction, Name: "DoStuff"})
	g.AddEdge("file:lib.go", "func:DoStuff", graph.EdgeContains)
	g.AddNode(&graph.Node{ID: "func:helper", Kind: graph.KindFunction, Name: "helper"})
	g.AddEdge("file:lib.go", "func:helper", graph.EdgeContains)

	// Without includeExported: exported DoStuff excluded, unexported helper reported.
	dead := FindDeadCode(g, false)
	foundHelper := false
	for _, d := range dead {
		if d.Name == "DoStuff" {
			t.Errorf("DoStuff should be excluded (exported in non-main package)")
		}
		if d.Name == "helper" {
			foundHelper = true
		}
	}
	if !foundHelper {
		t.Errorf("helper should be dead (unexported, unreferenced)")
	}

	// With includeExported: both reported.
	dead = FindDeadCode(g, true)
	foundDoStuff := false
	for _, d := range dead {
		if d.Name == "DoStuff" {
			foundDoStuff = true
		}
	}
	if !foundDoStuff {
		t.Errorf("DoStuff should be dead when includeExported=true")
	}
}

func TestFindDeadCode_UnusedType(t *testing.T) {
	g := buildTestGraph()

	// Add an unused type.
	g.AddNode(&graph.Node{
		ID: "type:Orphan", Kind: graph.KindType, Name: "Orphan",
		File: "main.go", Line: 30, Text: "struct{}",
	})
	g.AddEdge("file:main.go", "type:Orphan", graph.EdgeContains)

	dead := FindDeadCode(g, true)

	found := false
	for _, d := range dead {
		if d.Name == "Orphan" {
			found = true
		}
	}
	if !found {
		t.Errorf("Orphan type should be dead")
	}
}
