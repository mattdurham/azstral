// Package codegen renders a graph into Go source code.
// SPEC-010: The codegen package generates Go source files from graph nodes.
package codegen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/matt/azstral/graph"
	"github.com/matt/azstral/store"
)

// RenderFile generates Go source code for a file node in the graph.
// It walks the file's children (imports, comments, functions, types, variables)
// and emits valid Go source. Spec comments are pulled from the store.
func RenderFile(g *graph.Graph, st *store.Store, fileID string) (string, error) {
	fileNode, ok := g.GetNode(fileID)
	if !ok {
		return "", fmt.Errorf("file node %q not found", fileID)
	}
	if fileNode.Kind != graph.KindFile {
		return "", fmt.Errorf("node %q is %s, not file", fileID, fileNode.Kind)
	}

	// Find the parent package.
	pkgName := findPackageName(g, fileID)

	var b strings.Builder

	// Render file-level comments (those attached to the file node).
	renderComments(g, st, fileID, &b)

	// If a blank line separates the pre-package comment from `package`, reproduce it.
	if fileNode.Metadata["pre_package_blank"] == "true" {
		b.WriteString("\n")
	}

	// Package declaration.
	b.WriteString("package ")
	b.WriteString(pkgName)
	b.WriteString("\n")

	// Collect children — imports separate, rest in source line order.
	// NOTE: filter to nodes whose File field matches this file to guard against
	// spurious cross-file edges created when multiple packages share a symbol name.
	children := g.Children(fileID)
	var imports []*graph.Node
	var decls []*graph.Node
	for _, child := range children {
		if child.File != "" && child.File != fileID {
			continue
		}
		switch child.Kind {
		case graph.KindImport:
			imports = append(imports, child)
		case graph.KindFunction, graph.KindType, graph.KindVariable:
			decls = append(decls, child)
		}
	}

	// Collect pre-import comments (comments attached to import nodes, sorted by line).
	// These appear between the package declaration and the import block in the source.
	var importComments []*graph.Node
	seen := map[string]bool{}
	for _, imp := range imports {
		for _, e := range g.EdgesTo(imp.ID) {
			if e.Kind != graph.EdgeAnnotates {
				continue
			}
			cn, ok := g.GetNode(e.From)
			if !ok || cn.Kind != graph.KindComment || cn.Metadata["trailing"] == "true" {
				continue
			}
			if !seen[cn.ID] {
				seen[cn.ID] = true
				importComments = append(importComments, cn)
			}
		}
	}
	sort.Slice(importComments, func(i, j int) bool {
		return importComments[i].Line < importComments[j].Line
	})

	// Render import section (pre-import comments + import block).
	if len(imports) > 0 || len(importComments) > 0 {
		b.WriteString("\n")
		// Pre-import comments.
		prevCLine := 0
		for _, cn := range importComments {
			if prevCLine > 0 && cn.Line > prevCLine+1 {
				b.WriteString("\n")
			}
			for _, line := range strings.Split(cn.Text, "\n") {
				b.WriteString(line)
				b.WriteString("\n")
			}
			prevCLine = cn.EndLine
		}
		// Import block itself.
		if len(imports) == 1 && imports[0].Metadata["alias"] == "" && imports[0].Metadata["grouped"] != "true" {
			b.WriteString("import \"")
			b.WriteString(imports[0].Name)
			b.WriteString("\"\n")
		} else if len(imports) > 0 {
			b.WriteString("import (\n")
			prevImpLine := 0
			for _, imp := range imports {
				// Preserve blank lines between import groups (e.g. stdlib vs third-party).
				if prevImpLine > 0 && imp.Line > prevImpLine+1 {
					b.WriteString("\n")
				}
				alias := imp.Metadata["alias"]
				path := imp.Name
				if alias != "" && alias != pathBase(path) {
					b.WriteString("\t")
					b.WriteString(alias)
					b.WriteString(" ")
				} else {
					b.WriteString("\t")
				}
				b.WriteString("\"")
				b.WriteString(path)
				b.WriteString("\"\n")
				prevImpLine = imp.Line
			}
			b.WriteString(")\n")
		}
	}

	// Determine the end line of the import block for blank-line tracking.
	prevEndLine := 0
	if len(imports) > 0 {
		for _, imp := range imports {
			if bel := imp.Metadata["block_end_line"]; bel != "" {
				if n, err := strconv.Atoi(bel); err == nil && n > prevEndLine {
					prevEndLine = n
				}
			}
			if imp.Line > prevEndLine {
				prevEndLine = imp.Line
			}
		}
	}

	// All declarations (types, variables, functions) rendered in source line order.
	for _, node := range decls {
		// Determine the effective start line of this group (earliest comment or node line).
		startLine := node.Line
		for _, e := range g.EdgesTo(node.ID) {
			if e.Kind != graph.EdgeAnnotates {
				continue
			}
			cn, ok := g.GetNode(e.From)
			if !ok || cn.Kind != graph.KindComment || cn.Metadata["trailing"] == "true" {
				continue
			}
			if cn.Line < startLine {
				startLine = cn.Line
			}
		}

		// Emit blank line only when the source had one before this declaration.
		if prevEndLine == 0 || startLine > prevEndLine+1 {
			b.WriteString("\n")
		}

		renderComments(g, st, node.ID, &b)

		switch node.Kind {
		case graph.KindType:
			b.WriteString("type ")
			b.WriteString(node.Name)
			if tp := node.Metadata["type_params"]; tp != "" {
				b.WriteString(tp)
			}
			b.WriteString(" ")
			if node.Text != "" {
				b.WriteString(node.Text)
			} else {
				b.WriteString("struct{}")
			}
			b.WriteString("\n")

		case graph.KindVariable:
			if node.Metadata["raw"] == "true" {
				// Grouped const/var block — output verbatim.
				b.WriteString(node.Text)
				b.WriteString("\n")
			} else {
				keyword := "var"
				if node.Metadata["const"] == "true" {
					keyword = "const"
				}
				b.WriteString(keyword)
				b.WriteString(" ")
				b.WriteString(node.Name)
				if typeName := node.Metadata["type"]; typeName != "" {
					b.WriteString(" ")
					b.WriteString(typeName)
				}
				if node.Text != "" {
					b.WriteString(" = ")
					b.WriteString(node.Text)
				}
				b.WriteString("\n")
			}

		case graph.KindFunction:
			// Function signature.
			receiver := node.Metadata["receiver"]
			b.WriteString("func ")
			if receiver != "" {
				b.WriteString("(")
				b.WriteString(receiver)
				b.WriteString(") ")
			}
			b.WriteString(node.Name)
			if tp := node.Metadata["type_params"]; tp != "" {
				b.WriteString(tp)
			}
			params := node.Metadata["params"]
			if params == "" {
				params = "()"
			}
			b.WriteString(params)
			returns := node.Metadata["returns"]
			if returns != "" {
				b.WriteString(" ")
				b.WriteString(returns)
			}
			b.WriteString(" {\n")

			// Function body: statement tree if available, Text for manually-built nodes.
			body, ok := RenderBody(g, node.ID)
			if !ok {
				body = node.Text
			}
			for _, line := range strings.Split(body, "\n") {
				if line == "" {
					b.WriteString("\n")
				} else {
					b.WriteString("\t")
					b.WriteString(line)
					b.WriteString("\n")
				}
			}
			b.WriteString("}\n")
		}

		// Update prevEndLine.
		endLine := node.EndLine
		if endLine == 0 {
			endLine = node.Line
		}
		if endLine > prevEndLine {
			prevEndLine = endLine
		}
	}

	// Trailing comments: file-level comments that appear after all declarations.
	for _, e := range g.EdgesTo(fileID) {
		if e.Kind != graph.EdgeAnnotates {
			continue
		}
		cn, ok := g.GetNode(e.From)
		if !ok || cn.Kind != graph.KindComment || cn.Metadata["trailing"] != "true" {
			continue
		}
		b.WriteString("\n")
		for _, line := range strings.Split(cn.Text, "\n") {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	return b.String(), nil
}

// renderComments writes comment nodes and spec annotations attached to a node.
// Trailing comments (metadata["trailing"]=="true") are skipped here and rendered
// separately at the end of the file. Blank lines between comment groups are preserved.
func renderComments(g *graph.Graph, st *store.Store, nodeID string, b *strings.Builder) {
	// Collect, sort by line, then render.
	var comments []*graph.Node
	for _, e := range g.EdgesTo(nodeID) {
		if e.Kind == graph.EdgeAnnotates {
			cn, ok := g.GetNode(e.From)
			if !ok || cn.Kind != graph.KindComment || cn.Metadata["trailing"] == "true" {
				continue
			}
			comments = append(comments, cn)
		}
	}
	sort.Slice(comments, func(i, j int) bool { return comments[i].Line < comments[j].Line })

	prevLine := 0
	for _, cn := range comments {
		// Preserve blank lines between separate comment groups.
		if prevLine > 0 && cn.Line > prevLine+1 {
			b.WriteString("\n")
		}
		for _, line := range strings.Split(cn.Text, "\n") {
			// Lines are raw (include // prefix); write them directly.
			b.WriteString(line)
			b.WriteString("\n")
		}
		prevLine = cn.EndLine
	}

	// Find specs linked to this node in the store and render as comments.
	if st != nil {
		specs, _ := st.GetSpecsForNode(nodeID)
		for _, sp := range specs {
			b.WriteString("// ")
			b.WriteString(sp.ID)
			b.WriteString(": ")
			b.WriteString(sp.Title)
			b.WriteString("\n")
		}
	}
}

// findPackageName finds the package name for a file node by traversing Contains edges.
func findPackageName(g *graph.Graph, fileID string) string {
	for _, e := range g.EdgesTo(fileID) {
		if e.Kind == graph.EdgeContains {
			if n, ok := g.GetNode(e.From); ok && n.Kind == graph.KindPackage {
				return n.Name
			}
		}
	}
	return "main"
}

func pathBase(importPath string) string {
	parts := strings.Split(importPath, "/")
	return parts[len(parts)-1]
}
