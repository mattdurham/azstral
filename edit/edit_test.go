package edit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFunctionBody_PackageLevel(t *testing.T) {
	src := `package main

func hello() {
	println("old")
}

func other() {}
`
	path := writeTmp(t, src)
	if err := FunctionBody(path, "hello", "", `println("new")`); err != nil {
		t.Fatal(err)
	}
	got := readTmp(t, path)
	if !strings.Contains(got, `println("new")`) {
		t.Errorf("new body not found:\n%s", got)
	}
	if strings.Contains(got, `println("old")`) {
		t.Errorf("old body still present:\n%s", got)
	}
	if !strings.Contains(got, "func other()") {
		t.Errorf("other function missing:\n%s", got)
	}
}

func TestFunctionBody_Method(t *testing.T) {
	src := `package main

type Foo struct{}

func (f *Foo) Bar() string {
	return "old"
}
`
	path := writeTmp(t, src)
	if err := FunctionBody(path, "Bar", "*Foo", `return "new"`); err != nil {
		t.Fatal(err)
	}
	got := readTmp(t, path)
	if !strings.Contains(got, `return "new"`) {
		t.Errorf("new body not found:\n%s", got)
	}
	if strings.Contains(got, `return "old"`) {
		t.Errorf("old body still present:\n%s", got)
	}
}

func TestFunctionBody_ReceiverMatchByTypeName(t *testing.T) {
	src := `package main

type Graph struct{}

func (g *Graph) AddNode() error {
	return nil
}
`
	path := writeTmp(t, src)
	// receiver stored in graph node as "g *Graph"
	if err := FunctionBody(path, "AddNode", "g *Graph", `return fmt.Errorf("new")`); err != nil {
		t.Fatal(err)
	}
	got := readTmp(t, path)
	if !strings.Contains(got, `return fmt.Errorf("new")`) {
		t.Errorf("body not updated:\n%s", got)
	}
}

func TestFunctionBody_NotFound(t *testing.T) {
	src := "package main\nfunc foo() {}\n"
	path := writeTmp(t, src)
	err := FunctionBody(path, "nonexistent", "", "")
	if err == nil {
		t.Error("expected error for missing function")
	}
}

func TestFunctionBody_MultilineBody(t *testing.T) {
	src := `package main

func compute(x int) int {
	return x
}
`
	path := writeTmp(t, src)
	newBody := "if x > 0 {\n\treturn x * 2\n}\nreturn 0"
	if err := FunctionBody(path, "compute", "", newBody); err != nil {
		t.Fatal(err)
	}
	got := readTmp(t, path)
	if !strings.Contains(got, "return x * 2") {
		t.Errorf("multiline body not written:\n%s", got)
	}
}

func TestAppendFunction(t *testing.T) {
	src := "package main\n\nfunc existing() {}\n"
	path := writeTmp(t, src)
	if err := AppendFunction(path, "newFunc", "", "(x int)", "int", "return x + 1"); err != nil {
		t.Fatal(err)
	}
	got := readTmp(t, path)
	if !strings.Contains(got, "func newFunc(x int) int") {
		t.Errorf("function signature missing:\n%s", got)
	}
	if !strings.Contains(got, "return x + 1") {
		t.Errorf("function body missing:\n%s", got)
	}
	if !strings.Contains(got, "func existing()") {
		t.Errorf("existing function removed:\n%s", got)
	}
}

func TestAppendFunction_Method(t *testing.T) {
	src := "package main\n\ntype T struct{}\n"
	path := writeTmp(t, src)
	if err := AppendFunction(path, "Do", "t *T", "()", "error", "return nil"); err != nil {
		t.Fatal(err)
	}
	got := readTmp(t, path)
	if !strings.Contains(got, "func (t *T) Do() error") {
		t.Errorf("method signature missing:\n%s", got)
	}
}

func writeTmp(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readTmp(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
