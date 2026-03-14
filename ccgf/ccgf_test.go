package ccgf

import (
	"strings"
	"testing"

	"github.com/matt/azstral/graph"
)

// buildTestGraph creates a minimal graph resembling:
//
//	package main
//	import "fmt"
//	type Header struct{}
//	func ParseFile() Header { return readHeader() }
//	func readHeader() Header { return Header{} }
func buildTestGraph() *graph.Graph {
	g := graph.New()

	// Package.
	g.AddNode(&graph.Node{ID: "pkg:main", Kind: graph.KindPackage, Name: "main"})
	// File.
	g.AddNode(&graph.Node{ID: "file:main.go", Kind: graph.KindFile, Name: "main.go", File: "main.go", Line: 1})
	g.AddEdge("pkg:main", "file:main.go", graph.EdgeContains)

	// Import.
	g.AddNode(&graph.Node{ID: "import:fmt", Kind: graph.KindImport, Name: "fmt", File: "main.go", Line: 2})
	g.AddEdge("file:main.go", "import:fmt", graph.EdgeContains)

	// Type.
	g.AddNode(&graph.Node{ID: "type:Header", Kind: graph.KindType, Name: "Header", File: "main.go", Line: 3, Text: "struct{}"})
	g.AddEdge("file:main.go", "type:Header", graph.EdgeContains)

	// Function: ParseFile.
	g.AddNode(&graph.Node{
		ID: "func:ParseFile", Kind: graph.KindFunction, Name: "ParseFile",
		File: "main.go", Line: 4,
		Metadata: map[string]string{"returns": "Header"},
	})
	g.AddEdge("file:main.go", "func:ParseFile", graph.EdgeContains)

	// Function: readHeader.
	g.AddNode(&graph.Node{
		ID: "func:readHeader", Kind: graph.KindFunction, Name: "readHeader",
		File: "main.go", Line: 7,
		Metadata: map[string]string{"returns": "Header"},
	})
	g.AddEdge("file:main.go", "func:readHeader", graph.EdgeContains)

	// Call: ParseFile calls readHeader.
	g.AddNode(&graph.Node{ID: "call:func:ParseFile:0", Kind: graph.KindCall, Name: "readHeader()"})
	g.AddEdge("func:ParseFile", "call:func:ParseFile:0", graph.EdgeContains)
	g.AddEdge("call:func:ParseFile:0", "func:readHeader", graph.EdgeCallee)

	// Doc comment on ParseFile.
	g.AddNode(&graph.Node{
		ID: "comment:main.go:3", Kind: graph.KindComment, Name: "// ParseFile parses...",
		Text: "// ParseFile parses a file.\n// SPEC-001: Parse Go source.", Line: 3, EndLine: 4,
	})
	g.AddEdge("comment:main.go:3", "func:ParseFile", graph.EdgeAnnotates)

	// Spec node covering ParseFile.
	g.AddNode(&graph.Node{
		ID: "spec:SPEC-001", Kind: graph.KindSpec, Name: "SPEC-001",
		Metadata: map[string]string{"kind": "SPEC"},
	})
	g.AddEdge("spec:SPEC-001", "func:ParseFile", graph.EdgeCovers)

	// External package and function (vendor surface).
	g.AddNode(&graph.Node{ID: "pkg:fmt", Kind: graph.KindPackage, Name: "fmt", Metadata: map[string]string{"external": "true"}})
	g.AddNode(&graph.Node{ID: "func:fmt.Println", Kind: graph.KindFunction, Name: "Println", Metadata: map[string]string{"external": "true"}})

	// Call: ParseFile also calls fmt.Println.
	g.AddNode(&graph.Node{ID: "call:func:ParseFile:1", Kind: graph.KindCall, Name: "fmt.Println()"})
	g.AddEdge("func:ParseFile", "call:func:ParseFile:1", graph.EdgeContains)
	g.AddEdge("call:func:ParseFile:1", "func:fmt.Println", graph.EdgeCallee)
	g.AddEdge("call:func:ParseFile:1", "pkg:fmt", graph.EdgeReferences)

	return g
}

func TestEncodeBasic(t *testing.T) {
	g := buildTestGraph()
	out := Encode(g, Options{Module: "test"})

	// Header.
	if !strings.HasPrefix(out, "# ccgf1 scope=program vendor=surface mod=test\n") {
		t.Errorf("bad header:\n%s", out)
	}

	// Should contain project symbols.
	if !strings.Contains(out, " p main\n") {
		t.Errorf("missing package main:\n%s", out)
	}
	if !strings.Contains(out, " f main.ParseFile\n") {
		t.Errorf("missing func ParseFile:\n%s", out)
	}
	if !strings.Contains(out, " f main.readHeader\n") {
		t.Errorf("missing func readHeader:\n%s", out)
	}
	if !strings.Contains(out, " t main.Header\n") {
		t.Errorf("missing type Header:\n%s", out)
	}

	// Should contain vendor surface.
	if !strings.Contains(out, " p fmt V\n") {
		t.Errorf("missing vendor pkg fmt:\n%s", out)
	}
	if !strings.Contains(out, " f fmt.Println V\n") {
		t.Errorf("missing vendor func fmt.Println:\n%s", out)
	}

	// Should contain edges.
	if !strings.Contains(out, "d ") {
		t.Errorf("missing defines edges:\n%s", out)
	}
	if !strings.Contains(out, "c ") {
		t.Errorf("missing calls edges:\n%s", out)
	}

	t.Logf("output:\n%s", out)
}

func TestEncodeVendorInclude(t *testing.T) {
	g := buildTestGraph()
	out := Encode(g, Options{Vendor: VendorInclude})

	if !strings.Contains(out, "vendor=include") {
		t.Errorf("header should say vendor=include:\n%s", out)
	}
	if !strings.Contains(out, " p fmt V\n") {
		t.Errorf("vendor=include should still include fmt:\n%s", out)
	}
}

func TestEncodeFileScope(t *testing.T) {
	g := buildTestGraph()
	out := Encode(g, Options{Scope: "file:file:main.go"})

	if !strings.Contains(out, "scope=file:file:main.go") {
		t.Errorf("bad scope in header:\n%s", out)
	}
	// Should have the package and file's children.
	if !strings.Contains(out, " p main\n") {
		t.Errorf("missing package:\n%s", out)
	}
	if !strings.Contains(out, " f main.ParseFile") {
		t.Errorf("missing ParseFile:\n%s", out)
	}

	t.Logf("output:\n%s", out)
}

func TestEncodeTypeScope(t *testing.T) {
	g := buildTestGraph()

	// Add a method on Header.
	g.AddNode(&graph.Node{
		ID: "func:Header.String", Kind: graph.KindFunction, Name: "String",
		File: "main.go", Line: 10,
		Metadata: map[string]string{"receiver": "h Header", "returns": "string"},
	})
	g.AddEdge("file:main.go", "func:Header.String", graph.EdgeContains)

	out := Encode(g, Options{Scope: "type:type:Header"})

	if !strings.Contains(out, " t main.Header") {
		t.Errorf("missing type Header:\n%s", out)
	}
	if !strings.Contains(out, " m main.String") {
		t.Errorf("missing method String:\n%s", out)
	}
	// Should NOT contain ParseFile (not a method of Header).
	if strings.Contains(out, "ParseFile") {
		t.Errorf("should not contain ParseFile in type scope:\n%s", out)
	}

	t.Logf("output:\n%s", out)
}

func TestEncodeAttrs(t *testing.T) {
	g := buildTestGraph()
	out := Encode(g, Options{Attrs: true})

	if !strings.Contains(out, "a ") {
		t.Errorf("missing attributes:\n%s", out)
	}
	if !strings.Contains(out, " loc main.go:") {
		t.Errorf("missing loc attribute:\n%s", out)
	}
	if !strings.Contains(out, " sig func(") {
		t.Errorf("missing sig attribute:\n%s", out)
	}
	if !strings.Contains(out, " ro 1") {
		t.Errorf("missing ro attribute for vendor:\n%s", out)
	}

	t.Logf("output:\n%s", out)
}

func TestEncodeReturnEdge(t *testing.T) {
	g := buildTestGraph()
	out := Encode(g, Options{})

	// ParseFile returns Header → should have an 'r' edge.
	if !strings.Contains(out, "r ") {
		t.Errorf("missing return edge:\n%s", out)
	}
	t.Logf("output:\n%s", out)
}

func TestEncodeImportEdge(t *testing.T) {
	g := buildTestGraph()
	out := Encode(g, Options{})

	// main imports fmt → should have an 'm' edge.
	if !strings.Contains(out, "m ") {
		t.Errorf("missing import edge:\n%s", out)
	}
	t.Logf("output:\n%s", out)
}

func TestEncodeDocComment(t *testing.T) {
	g := buildTestGraph()
	out := Encode(g, Options{})

	// ParseFile has a doc comment → should appear as 'a sN doc ...'
	if !strings.Contains(out, " doc ParseFile parses a file.") {
		t.Errorf("missing doc attribute:\n%s", out)
	}
	t.Logf("output:\n%s", out)
}

func TestEncodeSpecIDs(t *testing.T) {
	g := buildTestGraph()
	out := Encode(g, Options{})

	// ParseFile is covered by SPEC-001 → should appear as 'a sN specs SPEC-001'
	if !strings.Contains(out, " specs SPEC-001") {
		t.Errorf("missing specs attribute:\n%s", out)
	}
	t.Logf("output:\n%s", out)
}
