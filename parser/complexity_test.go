package parser

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"

	"github.com/matt/azstral/graph"
)

func parseFunc(t *testing.T, src string) *ast.BlockStmt {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", "package main\n"+src, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			return fd.Body
		}
	}
	t.Fatal("no function found")
	return nil
}

func TestCyclomaticComplexity(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want int
	}{
		{"empty", "func f() {}", 1},
		{"single if", "func f() { if true {} }", 2},
		{"if-else", "func f() { if true {} else {} }", 2},
		{"if-else if", "func f(x int) { if x > 0 {} else if x < 0 {} }", 3},
		{"for loop", "func f() { for i := 0; i < 10; i++ {} }", 2},
		{"range", "func f() { for range []int{} {} }", 2},
		{"switch 2 cases", "func f(x int) { switch x { case 1: case 2: } }", 3},
		{"and-or", "func f(a, b bool) { if a && b || a {} }", 4},
		{"nested", "func f(x int) { if x > 0 { for i := range []int{} { if i > 0 {} } } }", 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := parseFunc(t, tt.src)
			got := CyclomaticComplexity(body)
			if got != tt.want {
				t.Errorf("CyclomaticComplexity = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCognitiveComplexity(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want int
	}{
		{"empty", "func f() {}", 0},
		{"single if", "func f() { if true {} }", 1},
		{"if-else", "func f() { if true {} else {} }", 2},
		{"nested if", "func f() { if true { if true {} } }", 3}, // 1 + (1+1)
		{"for", "func f() { for i := 0; i < 10; i++ {} }", 1},
		{"nested for-if", "func f() { for i := 0; i < 10; i++ { if true {} } }", 3}, // 1 + (1+1)
		{"bool ops", "func f(a, b bool) { if a && b {} }", 2},                        // 1(if) + 1(&&)
		{"switch", "func f(x int) { switch x { case 1: case 2: } }", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := parseFunc(t, tt.src)
			got := CognitiveComplexity(body)
			if got != tt.want {
				t.Errorf("CognitiveComplexity = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestComplexity_InGraph(t *testing.T) {
	src := `package main

func simple() {}

func complex(x int) int {
	if x > 0 {
		for i := 0; i < x; i++ {
			if i%2 == 0 {
				return i
			}
		}
	}
	return 0
}
`
	dir := t.TempDir()
	path := dir + "/main.go"
	os.WriteFile(path, []byte(src), 0o644)

	g := graph.New()
	if err := ParseFile(g, path); err != nil {
		t.Fatal(err)
	}

	simpleNode, ok := g.GetNode("func:main.simple")
	if !ok {
		t.Fatal("missing func:main.simple")
	}
	if simpleNode.Metadata["cyclomatic"] != "1" {
		t.Errorf("simple cyclomatic = %s, want 1", simpleNode.Metadata["cyclomatic"])
	}

	complexNode, ok := g.GetNode("func:main.complex")
	if !ok {
		t.Fatal("missing func:main.complex")
	}
	cyc := complexNode.Metadata["cyclomatic"]
	if cyc != "4" {
		t.Errorf("complex cyclomatic = %s, want 4", cyc)
	}
	cog := complexNode.Metadata["cognitive"]
	if cog == "0" || cog == "" {
		t.Errorf("complex cognitive should be > 0, got %s", cog)
	}
	t.Logf("complex: cyclomatic=%s cognitive=%s", cyc, cog)
}
