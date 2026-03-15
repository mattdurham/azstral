// Package edit provides surgical in-place edits to Go source files.
// Rather than re-rendering an entire file from the graph, these functions
// patch only the changed region, keeping the file as the source of truth.
package edit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"os"
	"strings"
)

// FunctionBody replaces the body of a named function in a Go source file.
// name is the function name; receiver is the receiver type string (e.g. "*Graph"),
// or empty for package-level functions. newBody is the body text as stored in
// the graph node — no surrounding braces, one-tab indent stripped.
func FunctionBody(filePath, name, receiver, newBody string) error {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", filePath, err)
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		return fmt.Errorf("parse %s: %w", filePath, err)
	}

	target := findFunc(f, name, receiver)
	if target == nil || target.Body == nil {
		return fmt.Errorf("function %s not found in %s", qualName(name, receiver), filePath)
	}

	open := fset.Position(target.Body.Lbrace).Offset
	close := fset.Position(target.Body.Rbrace).Offset

	body := buildBody(newBody)

	out := make([]byte, 0, len(src)+len(body))
	out = append(out, src[:open]...)
	out = append(out, body...)
	out = append(out, src[close+1:]...)

	return os.WriteFile(filePath, out, 0o644)
}

// AppendFunction appends a new function declaration to a Go source file.
// params and returns are raw text (e.g. "(x int, y int)" and "(int, error)").
// body is the function body text as stored in the graph node.
func AppendFunction(filePath, name, receiver, params, returns, body string) error {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", filePath, err)
	}

	var sig strings.Builder
	sig.WriteString("\nfunc ")
	if receiver != "" {
		sig.WriteByte('(')
		sig.WriteString(receiver)
		sig.WriteString(") ")
	}
	sig.WriteString(name)
	if params == "" || params == "()" {
		sig.WriteString("()")
	} else if strings.HasPrefix(params, "(") {
		sig.WriteString(params)
	} else {
		sig.WriteByte('(')
		sig.WriteString(params)
		sig.WriteByte(')')
	}
	if returns != "" {
		sig.WriteByte(' ')
		sig.WriteString(returns)
	}
	sig.WriteByte(' ')
	sig.WriteString(string(buildBody(body)))
	sig.WriteByte('\n')

	out := append(src, []byte(sig.String())...)
	return os.WriteFile(filePath, out, 0o644)
}

// RenameIdentifier replaces all occurrences of oldName with newName in a Go
// source file using go/ast for precision — only actual identifiers are matched,
// not substrings inside strings or comments. Returns the number of replacements.
func RenameIdentifier(filePath, oldName, newName string) (int, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", filePath, err)
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", filePath, err)
	}

	// Collect byte offsets of all matching identifiers, sorted in reverse so
	// replacements don't shift the offsets of subsequent positions.
	var offsets []int
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if ok && id.Name == oldName {
			offsets = append(offsets, fset.Position(id.Pos()).Offset)
		}
		return true
	})
	if len(offsets) == 0 {
		return 0, nil
	}

	// Sort descending so each replacement doesn't shift later offsets.
	sort.Sort(sort.Reverse(sort.IntSlice(offsets)))

	oldBytes := []byte(oldName)
	newBytes := []byte(newName)
	out := make([]byte, len(src))
	copy(out, src)
	for _, off := range offsets {
		if off+len(oldBytes) > len(out) {
			continue
		}
		out = append(out[:off], append(newBytes, out[off+len(oldBytes):]...)...)
	}

	if err := os.WriteFile(filePath, out, 0o644); err != nil {
		return 0, err
	}
	return len(offsets), nil
}

// findFunc locates a function declaration by name and receiver type.
func findFunc(f *ast.File, name, receiver string) *ast.FuncDecl {
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != name {
			continue
		}
		if receiver == "" {
			if fd.Recv == nil || len(fd.Recv.List) == 0 {
				return fd
			}
			continue
		}
		// Match receiver type — normalize pointer prefix for comparison.
		if fd.Recv == nil || len(fd.Recv.List) == 0 {
			continue
		}
		fdRecv := recvTypeName(fd.Recv.List[0].Type)
		want := strings.TrimLeft(receiver, "* ")
		// Strip variable name from receiver (e.g. "g *Graph" → "Graph").
		if idx := strings.LastIndex(want, " "); idx >= 0 {
			want = strings.TrimLeft(want[idx+1:], "*")
		} else {
			want = strings.TrimLeft(want, "*")
		}
		if fdRecv == want {
			return fd
		}
	}
	return nil
}

func recvTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return recvTypeName(t.X)
	case *ast.IndexExpr: // generic: T[P]
		return recvTypeName(t.X)
	case *ast.IndexListExpr: // generic: T[A, B]
		return recvTypeName(t.X)
	}
	return ""
}

// buildBody converts graph node body text back to a brace-enclosed function body.
func buildBody(body string) []byte {
	var b strings.Builder
	b.WriteString("{\n")
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			b.WriteByte('\n')
		} else {
			b.WriteByte('\t')
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	b.WriteByte('}')
	return []byte(b.String())
}

func qualName(name, receiver string) string {
	if receiver == "" {
		return name
	}
	return receiver + "." + name
}
