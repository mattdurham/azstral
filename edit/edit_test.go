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

func TestTypeBody_Struct(t *testing.T) {
	src := `package main

type Foo struct {
	X int
}

type Bar interface{ Do() }
`
	path := writeTmp(t, src)
	if err := TypeBody(path, "Foo", "struct {\n\tX int\n\tY string\n}"); err != nil {
		t.Fatal(err)
	}
	got := readTmp(t, path)
	if !strings.Contains(got, "Y string") {
		t.Errorf("new field not present:\n%s", got)
	}
	if strings.Contains(got, "X int\n}") {
		t.Errorf("old struct body still present:\n%s", got)
	}
	// Bar must be untouched.
	if !strings.Contains(got, "type Bar interface{ Do() }") {
		t.Errorf("Bar was modified:\n%s", got)
	}
}

func TestTypeBody_Interface(t *testing.T) {
	src := "package main\n\ntype Doer interface{ Old() }\n"
	path := writeTmp(t, src)
	if err := TypeBody(path, "Doer", "interface{ New() error }"); err != nil {
		t.Fatal(err)
	}
	got := readTmp(t, path)
	if !strings.Contains(got, "New() error") {
		t.Errorf("new method not present:\n%s", got)
	}
}

func TestTypeBody_NotFound(t *testing.T) {
	src := "package main\ntype Foo struct{}\n"
	path := writeTmp(t, src)
	if err := TypeBody(path, "Nonexistent", "struct{}"); err == nil {
		t.Error("expected error for missing type")
	}
}

func TestRenameIdentifier_Function(t *testing.T) {
	src := `package main

func OldName() {}

func caller() {
	OldName()
	x := OldName
	_ = x
}
`
	path := writeTmp(t, src)
	n, err := RenameIdentifier(path, "OldName", "NewName")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 { // definition + 2 usages
		t.Errorf("expected 3 replacements, got %d", n)
	}
	got := readTmp(t, path)
	if strings.Contains(got, "OldName") {
		t.Errorf("OldName still present:\n%s", got)
	}
	if !strings.Contains(got, "func NewName()") {
		t.Errorf("definition not renamed:\n%s", got)
	}
}

func TestRenameIdentifier_Type(t *testing.T) {
	src := `package main

type OldType struct{ X int }

func New() OldType       { return OldType{} }
func Use(t OldType) int { return t.X }
`
	path := writeTmp(t, src)
	n, err := RenameIdentifier(path, "OldType", "NewType")
	if err != nil {
		t.Fatal(err)
	}
	if n < 4 {
		t.Errorf("expected at least 4 replacements, got %d", n)
	}
	got := readTmp(t, path)
	if strings.Contains(got, "OldType") {
		t.Errorf("OldType still present:\n%s", got)
	}
}

func TestRenameIdentifier_NoMatch(t *testing.T) {
	src := "package main\nfunc foo() {}\n"
	path := writeTmp(t, src)
	n, err := RenameIdentifier(path, "bar", "baz")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 replacements, got %d", n)
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
