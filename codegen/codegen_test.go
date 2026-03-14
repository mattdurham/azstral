// Package codegen_test contains round-trip tests for the codegen package.
// Each test parses a Go source file into a graph and renders it back, then
// asserts byte-for-byte equality with the original source.
package codegen_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt/azstral/codegen"
	"github.com/matt/azstral/graph"
	"github.com/matt/azstral/parser"
)

// testRoundTrip writes src to a temp file, parses it, renders it, and asserts
// that the output matches src exactly. On mismatch it prints a line-by-line diff.
func testRoundTrip(t *testing.T, src string) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "input.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	g := graph.New()
	if err := parser.ParseFile(g, path); err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	fileID := "file:" + path
	got, err := codegen.RenderFile(g, nil, fileID)
	if err != nil {
		t.Fatalf("RenderFile: %v", err)
	}

	if got == src {
		return
	}

	// Line-by-line diff for readable failure output.
	wantLines := strings.Split(src, "\n")
	gotLines := strings.Split(got, "\n")
	maxLen := max(len(wantLines), len(gotLines))
	t.Errorf("round-trip mismatch (want vs got):")
	for i := range maxLen {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			t.Errorf("  line %d:\n    want: %q\n    got:  %q", i+1, w, g)
		}
	}
}

// ---------------------------------------------------------------------------
// GoByExample round-trip tests
// ---------------------------------------------------------------------------

func TestRoundTrip_GoByExample(t *testing.T) {
	baseDir := "/home/matt/source/gobyexample/examples"
	if _, err := os.Stat(baseDir); err != nil {
		t.Skipf("gobyexample directory not found at %s: %v", baseDir, err)
	}

	slugs := []struct {
		slug string
		file string // relative to baseDir/<slug>/
	}{
		{"hello-world", "hello-world.go"},
		{"functions", "functions.go"},
		{"enums", "enums.go"},
		{"interfaces", "interfaces.go"},
		{"generics", "generics.go"},
		{"errors", "errors.go"},
		{"recover", "recover.go"},
		{"logging", "logging.go"},
		{"embed-directive", "embed-directive.go"},
		{"strings-and-runes", "strings-and-runes.go"},
		{"custom-errors", "custom-errors.go"},
	}

	for _, tc := range slugs {
		t.Run(tc.slug, func(t *testing.T) {
			path := filepath.Join(baseDir, tc.slug, tc.file)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("source file not found: %v", err)
			}

			g := graph.New()
			if err := parser.ParseFile(g, path); err != nil {
				t.Fatalf("ParseFile: %v", err)
			}

			fileID := "file:" + path
			got, err := codegen.RenderFile(g, nil, fileID)
			if err != nil {
				t.Fatalf("RenderFile: %v", err)
			}

			want := string(src)
			if got == want {
				return
			}

			wantLines := strings.Split(want, "\n")
			gotLines := strings.Split(got, "\n")
			maxLen := max(len(wantLines), len(gotLines))
			t.Errorf("round-trip mismatch for %s:", tc.slug)
			for i := range maxLen {
				var w, g string
				if i < len(wantLines) {
					w = wantLines[i]
				}
				if i < len(gotLines) {
					g = gotLines[i]
				}
				if w != g {
					t.Errorf("  line %d:\n    want: %q\n    got:  %q", i+1, w, g)
				}
			}
			// Also print full output for context when debugging.
			t.Logf("full rendered output:\n%s", got)
		})
	}
}

// ---------------------------------------------------------------------------
// Individual behaviour tests using inline source strings
// ---------------------------------------------------------------------------

func TestRoundTrip_GroupedConst(t *testing.T) {
	src := `package main

import "fmt"

// Weekday is a day of the week.
type Weekday int

const (
	Sunday Weekday = iota
	Monday
	Tuesday
	Wednesday
	Thursday
	Friday
	Saturday
)

func main() {
	fmt.Println(Monday)
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_GroupedVar(t *testing.T) {
	src := `package main

import "fmt"

var (
	hostname = "localhost"
	port     = 8080
	debug    = false
)

func main() {
	fmt.Println(hostname, port, debug)
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_SingleReturn(t *testing.T) {
	src := `package main

import "fmt"

func double(n int) int {
	return n * 2
}

func main() {
	fmt.Println(double(4))
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_MultiReturn(t *testing.T) {
	src := `package main

import (
	"errors"
	"fmt"
)

func divide(a, b float64) (float64, error) {
	if b == 0 {
		return 0, errors.New("division by zero")
	}
	return a / b, nil
}

func main() {
	v, err := divide(10, 3)
	fmt.Println(v, err)
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_GroupedParams(t *testing.T) {
	src := `package main

import "fmt"

func sum(a, b, c int) int {
	return a + b + c
}

func main() {
	fmt.Println(sum(1, 2, 3))
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_Variadic(t *testing.T) {
	src := `package main

import "fmt"

func total(nums ...int) int {
	n := 0
	for _, v := range nums {
		n += v
	}
	return n
}

func main() {
	fmt.Println(total(1, 2, 3, 4))
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_FuncTypeReturn(t *testing.T) {
	src := `package main

import "fmt"

func adder(x int) func(int) int {
	return func(y int) int {
		return x + y
	}
}

func main() {
	add5 := adder(5)
	fmt.Println(add5(3))
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_ReceiverMethod(t *testing.T) {
	src := `package main

import "fmt"

type rect struct {
	width, height float64
}

func (r rect) area() float64 {
	return r.width * r.height
}

func (r rect) perim() float64 {
	return 2*r.width + 2*r.height
}

func main() {
	r := rect{width: 3, height: 4}
	fmt.Println(r.area())
	fmt.Println(r.perim())
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_GenericType(t *testing.T) {
	src := `package main

import "fmt"

type Stack[T any] struct {
	items []T
}

func (s *Stack[T]) Push(v T) {
	s.items = append(s.items, v)
}

func (s *Stack[T]) Pop() (T, bool) {
	var zero T
	if len(s.items) == 0 {
		return zero, false
	}
	last := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return last, true
}

func main() {
	var s Stack[int]
	s.Push(1)
	s.Push(2)
	v, ok := s.Pop()
	fmt.Println(v, ok)
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_GenericFunc(t *testing.T) {
	src := `package main

import "fmt"

func Map[T, V any](ts []T, fn func(T) V) []V {
	result := make([]V, len(ts))
	for i, t := range ts {
		result[i] = fn(t)
	}
	return result
}

func main() {
	doubled := Map([]int{1, 2, 3}, func(n int) int { return n * 2 })
	fmt.Println(doubled)
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_ConsecutiveTypes(t *testing.T) {
	src := `package main

import "fmt"

// geometry is an interface for geometric shapes.
type geometry interface {
	area() float64
	perim() float64
}

// rect is a rectangle.
type rect struct {
	width, height float64
}
type circle struct {
	radius float64
}

func main() {
	fmt.Println(rect{3, 4})
	fmt.Println(circle{5})
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_VarBetweenFuncs(t *testing.T) {
	src := `package main

import (
	"errors"
	"fmt"
)

func first() error {
	return nil
}

var ErrNotFound = errors.New("not found")

func second() error {
	return ErrNotFound
}

func main() {
	fmt.Println(first())
	fmt.Println(second())
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_CommentGroupBlankLine(t *testing.T) {
	// Two pre-package comment groups separated by a blank line.
	// The parser sets pre_package_blank="true" on the file node
	// and codegen emits the blank line before `package`.
	src := `// Copyright 2024 Example Corp.
// All rights reserved.

// Package main is a demonstration.
package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_PreImportComment(t *testing.T) {
	// A comment that lives between the package declaration and the import block.
	src := `// Package main demonstrates pre-import comments.
package main

// Import the fmt package for output.
import "fmt"

func main() {
	fmt.Println("hi")
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_GoEmbedDirective(t *testing.T) {
	// //go:embed directives have no space after // — they must be preserved verbatim.
	src := `package main

import (
	"embed"
)

//go:embed testdata/hello.txt
var greeting string

//go:embed testdata/*.txt
var folder embed.FS

func main() {
	print(greeting)
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_BlankCommentLine(t *testing.T) {
	// A // blank comment line (just "//") within a doc comment.
	src := `package main

import "fmt"

// greet prints a greeting.
//
// It always says hello.
func greet(name string) {
	fmt.Println("hello,", name)
}

func main() {
	greet("world")
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_ImportGroupBlank(t *testing.T) {
	// A blank line within the import block separates stdlib from third-party.
	src := `package main

import (
	"fmt"
	"os"

	"github.com/matt/azstral/graph"
)

func main() {
	g := graph.New()
	fmt.Println(g, os.Args)
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_SingleImportWithParens(t *testing.T) {
	// A single import written with parentheses must render with parentheses.
	src := `package main

import (
	"fmt"
)

func main() {
	fmt.Println("hello")
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_PrePackageBlank(t *testing.T) {
	// A blank line between the file comment and the package keyword.
	src := `// Copyright notice for the file.

package main

import "fmt"

func main() {
	fmt.Println("ok")
}
`
	testRoundTrip(t, src)
}

func TestRoundTrip_TrailingComment(t *testing.T) {
	// A comment that appears after all declarations (trailing file-level comment).
	src := `package main

import "fmt"

func main() {
	fmt.Println("done")
}

// end of file
`
	testRoundTrip(t, src)
}

func TestRoundTrip_ImportAlias(t *testing.T) {
	// An import with an alias that differs from the package base name.
	src := `package main

import (
	b64 "encoding/base64"
	"fmt"
)

func main() {
	encoded := b64.StdEncoding.EncodeToString([]byte("abc"))
	fmt.Println(encoded)
}
`
	testRoundTrip(t, src)
}

// TestRenderFile_CrossFileFilter verifies that RenderFile does not emit symbols
// from other files even if a spurious contains edge exists in the graph.
func TestRenderFile_CrossFileFilter(t *testing.T) {
	dir := t.TempDir()

	// File A defines type Foo.
	pathA := filepath.Join(dir, "a.go")
	os.WriteFile(pathA, []byte("package p\n\ntype Foo struct{}\n"), 0o644)

	// File B defines func Bar.
	pathB := filepath.Join(dir, "b.go")
	os.WriteFile(pathB, []byte("package p\n\nfunc Bar() {}\n"), 0o644)

	g := graph.New()
	parser.ParseFile(g, pathA)
	parser.ParseFile(g, pathB)

	// Manually inject a spurious edge: file B "contains" type Foo from file A.
	fileB := "file:" + pathB
	typeA := "type:Foo"
	_ = g.AddEdge(fileB, typeA, graph.EdgeContains)

	// Render file B — Foo must NOT appear in the output.
	got, err := codegen.RenderFile(g, nil, fileB)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "type Foo") {
		t.Errorf("RenderFile leaked cross-file type Foo into file B output:\n%s", got)
	}
	if !strings.Contains(got, "func Bar") {
		t.Errorf("RenderFile missing Bar in file B output:\n%s", got)
	}
	t.Logf("output:\n%s", got)
}

// TestParseTree_RoundTrip verifies that parsing two packages with shared
// symbol names and rendering each file produces clean, non-contaminated output.
func TestParseTree_RoundTrip(t *testing.T) {
	root := t.TempDir()

	aDir := filepath.Join(root, "a")
	bDir := filepath.Join(root, "b")
	os.MkdirAll(aDir, 0o755)
	os.MkdirAll(bDir, 0o755)

	srcA := "package a\n\ntype Config struct{ X int }\n\nfunc New() *Config { return &Config{} }\n"
	srcB := "package b\n\ntype Config struct{ Y string }\n\nfunc New() *Config { return &Config{} }\n"
	os.WriteFile(filepath.Join(aDir, "a.go"), []byte(srcA), 0o644)
	os.WriteFile(filepath.Join(bDir, "b.go"), []byte(srcB), 0o644)

	g := graph.New()
	parser.ParseTree(g, root)

	// Render file A — must only contain package a's symbols.
	fileA := "file:" + filepath.Join(aDir, "a.go")
	gotA, err := codegen.RenderFile(g, nil, fileA)
	if err != nil {
		t.Fatalf("RenderFile a: %v", err)
	}
	if strings.Contains(gotA, "Y string") {
		t.Errorf("file A output contains package b's field Y:\n%s", gotA)
	}

	// Render file B — must only contain package b's symbols.
	fileB := "file:" + filepath.Join(bDir, "b.go")
	gotB, err := codegen.RenderFile(g, nil, fileB)
	if err != nil {
		t.Fatalf("RenderFile b: %v", err)
	}
	if strings.Contains(gotB, "X int") {
		t.Errorf("file B output contains package a's field X:\n%s", gotB)
	}

	t.Logf("A output:\n%s", gotA)
	t.Logf("B output:\n%s", gotB)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// fmtDiff returns a formatted line-by-line diff string (for use in Logf).
func fmtDiff(want, got string) string {
	var sb strings.Builder
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	max := max(len(wl), len(gl))
	for i := range max {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			fmt.Fprintf(&sb, "line %d:\n  want: %q\n  got:  %q\n", i+1, w, g)
		}
	}
	return sb.String()
}

// suppress unused warning
var _ = fmtDiff
