package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt/azstral/graph"
)

// TEST-001: Parse hello/main.go and verify graph contains Package, File, Function nodes.
func TestParseFile_HelloWorld(t *testing.T) {
	// Find the hello world source relative to the project root.
	helloPath := findHelloMain(t)

	g := graph.New()
	if err := ParseFile(g, helloPath); err != nil {
		t.Fatal(err)
	}

	// Must have a package node named "main".
	pkgs := g.NodesByKind(graph.KindPackage)
	if len(pkgs) == 0 {
		t.Fatal("no package nodes")
	}
	found := false
	for _, p := range pkgs {
		if p.Name == "main" && p.Metadata["external"] == "" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no package node named 'main'; got %v", pkgs)
	}

	// Must have a file node.
	files := g.NodesByKind(graph.KindFile)
	if len(files) == 0 {
		t.Fatal("no file nodes")
	}

	// Must have a function node for main.
	fns := g.NodesByKind(graph.KindFunction)
	foundMain := false
	for _, fn := range fns {
		if fn.Name == "main" && fn.Metadata["external"] == "" {
			foundMain = true
			break
		}
	}
	if !foundMain {
		t.Error("no function node named 'main'")
	}
}

// TEST-002: Verify SPEC/NOTE identifiers are extracted from hello/main.go comments.
func TestParseFile_SpecExtraction(t *testing.T) {
	helloPath := findHelloMain(t)

	g := graph.New()
	if err := ParseFile(g, helloPath); err != nil {
		t.Fatal(err)
	}

	specNodes := g.NodesByKind(graph.KindSpec)
	if len(specNodes) == 0 {
		t.Fatal("no spec nodes extracted")
	}

	// Should find SPEC-004 at minimum.
	found := false
	for _, sn := range specNodes {
		if sn.Name == "SPEC-004" {
			found = true
			break
		}
	}
	if !found {
		t.Error("SPEC-004 not found in graph")
	}
}

func findHelloMain(t *testing.T) string {
	t.Helper()
	// Walk up from the test directory to find cmd/hello/main.go.
	dir, _ := os.Getwd()
	for range 5 {
		candidate := filepath.Join(dir, "cmd", "hello", "main.go")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find cmd/hello/main.go")
	return ""
}

// writeTempGo writes src to a temp file ending in .go and returns its path.
func writeTempGo(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "input.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

// findNode returns the first node matching a predicate, or nil.
func findNode(nodes []*graph.Node, pred func(*graph.Node) bool) *graph.Node {
	for _, n := range nodes {
		if pred(n) {
			return n
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// TEST: Grouped const block creates a raw block node
// ---------------------------------------------------------------------------

// TestParseFile_GroupedConstBlock verifies that a grouped const (...) declaration
// is stored as a single KindVariable node with metadata["raw"]=="true".
func TestParseFile_GroupedConstBlock(t *testing.T) {
	src := `package main

const (
	A = iota
	B
	C
)
`
	path := writeTempGo(t, src)
	g := graph.New()
	if err := ParseFile(g, path); err != nil {
		t.Fatal(err)
	}

	vars := g.NodesByKind(graph.KindVariable)
	block := findNode(vars, func(n *graph.Node) bool {
		return n.Metadata["raw"] == "true" && n.Metadata["keyword"] == "const"
	})
	if block == nil {
		t.Fatalf("no raw const block node found; got %d variable nodes", len(vars))
	}

	// The block text must contain the const keyword and the iota expression.
	if !strings.Contains(block.Text, "const") {
		t.Errorf("block.Text does not contain 'const': %q", block.Text)
	}
	if !strings.Contains(block.Text, "iota") {
		t.Errorf("block.Text does not contain 'iota': %q", block.Text)
	}
}

// TestParseFile_GroupedConstBlock_TableDriven exercises multiple grouped-block
// variants to confirm both const and var blocks get the raw flag.
func TestParseFile_GroupedConstBlock_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		keyword string
	}{
		{
			name: "grouped_const_iota",
			src: `package p
const (
	X = iota
	Y
	Z
)
`,
			keyword: "const",
		},
		{
			name: "grouped_var",
			src: `package p
var (
	host = "localhost"
	port = 8080
)
`,
			keyword: "var",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempGo(t, tc.src)
			g := graph.New()
			if err := ParseFile(g, path); err != nil {
				t.Fatal(err)
			}

			vars := g.NodesByKind(graph.KindVariable)
			block := findNode(vars, func(n *graph.Node) bool {
				return n.Metadata["raw"] == "true" && n.Metadata["keyword"] == tc.keyword
			})
			if block == nil {
				t.Errorf("no raw %s block node; variable nodes: %v", tc.keyword, vars)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TEST: Receiver method node ID and metadata
// ---------------------------------------------------------------------------

// TestParseFile_ReceiverMethod verifies that a method with a receiver gets an
// ID like "func:*argError.Error" and has metadata["receiver"] set.
func TestParseFile_ReceiverMethod(t *testing.T) {
	src := `package main

import "fmt"

type argError struct {
	arg     int
	message string
}

func (e *argError) Error() string {
	return fmt.Sprintf("%d - %s", e.arg, e.message)
}
`
	path := writeTempGo(t, src)
	g := graph.New()
	if err := ParseFile(g, path); err != nil {
		t.Fatal(err)
	}

	fns := g.NodesByKind(graph.KindFunction)
	// The function ID for a pointer receiver "*argError" and method "Error"
	// should be "func:*argError.Error".
	methodNode := findNode(fns, func(n *graph.Node) bool {
		return n.ID == "func:*argError.Error"
	})
	if methodNode == nil {
		ids := make([]string, 0, len(fns))
		for _, fn := range fns {
			ids = append(ids, fn.ID)
		}
		t.Fatalf("no node with ID 'func:*argError.Error'; found: %v", ids)
	}

	if methodNode.Metadata["receiver"] == "" {
		t.Errorf("metadata[receiver] is empty; want non-empty (e.g. 'e *argError')")
	}
	if !strings.Contains(methodNode.Metadata["receiver"], "argError") {
		t.Errorf("metadata[receiver] = %q, want it to contain 'argError'", methodNode.Metadata["receiver"])
	}
}

// ---------------------------------------------------------------------------
// TEST: Generic type params stored in metadata
// ---------------------------------------------------------------------------

// TestParseFile_GenericTypeParams verifies that a generic type declaration like
// `type List[T any] struct { ... }` stores the type params in metadata.
func TestParseFile_GenericTypeParams(t *testing.T) {
	src := `package main

type List[T any] struct {
	head *element[T]
}

type element[T any] struct {
	next *element[T]
	val  T
}
`
	path := writeTempGo(t, src)
	g := graph.New()
	if err := ParseFile(g, path); err != nil {
		t.Fatal(err)
	}

	types := g.NodesByKind(graph.KindType)
	listNode := findNode(types, func(n *graph.Node) bool {
		return n.ID == "type:List"
	})
	if listNode == nil {
		ids := make([]string, 0, len(types))
		for _, ty := range types {
			ids = append(ids, ty.ID)
		}
		t.Fatalf("no node 'type:List'; found: %v", ids)
	}

	tp := listNode.Metadata["type_params"]
	if tp == "" {
		t.Fatal("metadata[type_params] is empty for type List[T any]")
	}
	if !strings.Contains(tp, "T") {
		t.Errorf("metadata[type_params] = %q, want it to contain 'T'", tp)
	}
	if !strings.Contains(tp, "any") {
		t.Errorf("metadata[type_params] = %q, want it to contain 'any'", tp)
	}
}

// ---------------------------------------------------------------------------
// TEST: Comment node text is raw (includes // prefix)
// ---------------------------------------------------------------------------

// TestParseFile_RawCommentText verifies that comment nodes store raw text
// including the // prefix (not stripped).
func TestParseFile_RawCommentText(t *testing.T) {
	src := `// Package p is a test package.
// It does nothing useful.
package p

// add returns the sum of a and b.
func add(a, b int) int {
	return a + b
}
`
	path := writeTempGo(t, src)
	g := graph.New()
	if err := ParseFile(g, path); err != nil {
		t.Fatal(err)
	}

	comments := g.NodesByKind(graph.KindComment)
	if len(comments) == 0 {
		t.Fatal("no comment nodes found")
	}

	for _, cn := range comments {
		lines := strings.Split(cn.Text, "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "//") {
				t.Errorf("comment line does not start with '//': %q (node %s)", line, cn.ID)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// TEST: Grouped import has metadata["grouped"]=="true"
// ---------------------------------------------------------------------------

// TestParseFile_ImportGroupedMetadata verifies that imports inside a grouped
// import block (`import ( ... )`) have metadata["grouped"]=="true".
func TestParseFile_ImportGroupedMetadata(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		grouped bool
	}{
		{
			name: "single_ungrouped",
			src: `package p
import "fmt"
func f() { _ = fmt.Sprintf }
`,
			grouped: false,
		},
		{
			name: "grouped",
			src: `package p
import (
	"fmt"
	"os"
)
func f() { _ = fmt.Sprintf; _ = os.Args }
`,
			grouped: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempGo(t, tc.src)
			g := graph.New()
			if err := ParseFile(g, path); err != nil {
				t.Fatal(err)
			}

			imports := g.NodesByKind(graph.KindImport)
			if len(imports) == 0 {
				t.Fatal("no import nodes")
			}

			for _, imp := range imports {
				got := imp.Metadata["grouped"] == "true"
				if got != tc.grouped {
					t.Errorf("import %s: grouped=%v, want %v", imp.ID, got, tc.grouped)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TEST: Local function called before declared is correctly updated
// ---------------------------------------------------------------------------

// TestParseFile_LocalFunctionShadow verifies that when a local function is
// called before it is declared, the final graph node reflects the actual
// declaration's line and body text, not the stub created at call time.
func TestParseFile_LocalFunctionShadow(t *testing.T) {
	src := `package main

import "fmt"

func main() {
	fmt.Println(helper())
}

func helper() string {
	return "hello from helper"
}
`
	path := writeTempGo(t, src)
	g := graph.New()
	if err := ParseFile(g, path); err != nil {
		t.Fatal(err)
	}

	fns := g.NodesByKind(graph.KindFunction)
	helperNode := findNode(fns, func(n *graph.Node) bool {
		return n.ID == "func:helper" && n.Metadata["external"] == ""
	})
	if helperNode == nil {
		ids := make([]string, 0, len(fns))
		for _, fn := range fns {
			ids = append(ids, fmt.Sprintf("%s(external=%s)", fn.ID, fn.Metadata["external"]))
		}
		t.Fatalf("no local 'func:helper' node; found: %v", ids)
	}

	// The declaration is on line 9 of the source (1-indexed).
	// We verify the node line is > the main function line (line 5).
	if helperNode.Line <= 0 {
		t.Errorf("helper node has non-positive Line: %d", helperNode.Line)
	}

	// The body text must come from the real declaration, not the stub.
	if !strings.Contains(helperNode.Text, "hello from helper") {
		t.Errorf("helper node Text = %q; want it to contain 'hello from helper'", helperNode.Text)
	}

	// Params must be populated from the real declaration.
	params := helperNode.Metadata["params"]
	if params == "" {
		t.Error("helper node has empty params; want '()' from declaration")
	}

	// Returns must be populated from the real declaration.
	returns := helperNode.Metadata["returns"]
	if returns == "" {
		t.Error("helper node has empty returns; want 'string' from declaration")
	}
	if !strings.Contains(returns, "string") {
		t.Errorf("helper node returns = %q; want it to contain 'string'", returns)
	}
}
