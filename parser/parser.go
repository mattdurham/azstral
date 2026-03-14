// Package parser converts Go source files into an azstral graph using go/ast.
// SPEC-001: Parse Go source into a directed graph.
// SPEC-005: Use go/ast, go/parser, go/token.
package parser

import (
	"go/ast"
	"go/parser"
	"go/token"

	"github.com/matt/azstral/graph"
	"github.com/matt/azstral/specs"

	"fmt"

	"os"
	"path/filepath"

	"strings"
)
// ParseFile parses a single Go file and adds its nodes/edges to the graph.
func ParseFile(g *graph.Graph, filePath string) error {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", filePath, err)
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", filePath, err)
	}

	pkgName := f.Name.Name
	pkgID := "pkg:" + pkgName
	fileID := "file:" + filePath

	// Package node (idempotent — ignore duplicate error).
	_ = g.AddNode(&graph.Node{
		ID:   pkgID,
		Kind: graph.KindPackage,
		Name: pkgName,
	})

	// Detect whether there's a blank line between the pre-package comment and
	// the `package` keyword. When f.Doc == nil but there are pre-package comments,
	// a blank line separates them — the codegen needs to reproduce that blank line.
	fileMeta := map[string]string{}
	if f.Doc == nil {
		for _, cg := range f.Comments {
			if cg.Pos() < f.Package {
				pkgLine := fset.Position(f.Package).Line
				commentEndLine := fset.Position(cg.End()).Line
				if pkgLine > commentEndLine+1 {
					fileMeta["pre_package_blank"] = "true"
				}
				break
			}
		}
	}

	// File node.
	if err := g.AddNode(&graph.Node{
		ID:       fileID,
		Kind:     graph.KindFile,
		Name:     filepath.Base(filePath),
		File:     filePath,
		Line:     fset.Position(f.Pos()).Line,
		Metadata: fileMeta,
	}); err != nil {
		return err
	}
	_ = g.AddEdge(pkgID, fileID, graph.EdgeContains)

	// Walk top-level declarations.
	ast.Inspect(f, func(n ast.Node) bool {
		switch decl := n.(type) {
		case *ast.FuncDecl:
			addFunction(g, fset, src, fileID, decl)
			return false // addFunction walks the body itself
		case *ast.GenDecl:
			addGenDecl(g, fset, src, fileID, decl)
		}
		return true
	})

	// Build a map from comment group start position → code node ID using the AST's
	// own doc-comment associations. This is authoritative: Go's parser links each
	// doc comment to the declaration it documents.
	docTarget := buildDocTargetMap(fset, f, fileID)

	// Build a set of source ranges covered by function bodies.
	// Comments inside function bodies are captured in fn.Text and must not be
	// processed as standalone file-level comment nodes.
	type bodyRange struct{ lo, hi token.Pos }
	var bodyRanges []bodyRange
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Body != nil {
			bodyRanges = append(bodyRanges, bodyRange{fd.Body.Lbrace, fd.Body.Rbrace})
		}
	}
	inBody := func(pos token.Pos) bool {
		for _, r := range bodyRanges {
			if pos > r.lo && pos < r.hi {
				return true
			}
		}
		return false
	}

	// Process all comment groups.
	for _, cg := range f.Comments {
		if inBody(cg.Pos()) {
			continue // already captured in function body text
		}

		// Store raw comment lines (with // prefix) to preserve exact formatting.
		var rawLines []string
		for _, c := range cg.List {
			rawLines = append(rawLines, c.Text)
		}
		rawText := strings.Join(rawLines, "\n")

		pos := fset.Position(cg.Pos())
		endPos := fset.Position(cg.End())

		// Use AST-resolved target first, then line proximity, then trailing fallback.
		target, ok := docTarget[cg.Pos()]
		if !ok {
			target = findNearestCodeNode(g, fileID, endPos.Line)
		}

		commentMeta := map[string]string{}
		if target == "" {
			// No code node follows this comment — it's a trailing file-level comment.
			target = fileID
			commentMeta["trailing"] = "true"
		}

		commentID := fmt.Sprintf("comment:%s:%d", filepath.Base(filePath), pos.Line)
		_ = g.AddNode(&graph.Node{
			ID:       commentID,
			Kind:     graph.KindComment,
			Name:     truncate(rawText, 60),
			File:     filePath,
			Line:     pos.Line,
			EndLine:  endPos.Line,
			Text:     rawText,
			Metadata: commentMeta,
		})
		_ = g.AddEdge(fileID, commentID, graph.EdgeContains)
		_ = g.AddEdge(commentID, target, graph.EdgeAnnotates)

		// Extract spec identifiers from comments.
		idents := specs.Extract(rawText)
		for _, ident := range idents {
			specID := "spec:" + ident.ID
			_ = g.AddNode(&graph.Node{
				ID:   specID,
				Kind: graph.KindSpec,
				Name: ident.ID,
				Metadata: map[string]string{
					"kind": string(ident.Kind),
				},
			})
			_ = g.AddEdge(commentID, specID, graph.EdgeAnnotates)

			if target != "" {
				_ = g.AddEdge(specID, target, graph.EdgeCovers)
			}
		}

		// Extract Go compiler directives (//go:embed, //go:build, etc.)
		// and attach as metadata on the target node.
		extractDirectives(g, cg, target)
	}

	return nil
}

// ParseDir parses all Go files in a directory.
func ParseDir(g *graph.Graph, dirPath string) error {
	matches, err := filepath.Glob(filepath.Join(dirPath, "*.go"))
	if err != nil {
		return fmt.Errorf("glob dir %s: %w", dirPath, err)
	}
	for _, filePath := range matches {
		if err := ParseFile(g, filePath); err != nil {
			return err
		}
	}
	return nil
}

func addFunction(g *graph.Graph, fset *token.FileSet, src []byte, fileID string, decl *ast.FuncDecl) {
	pos := fset.Position(decl.Pos())
	endPos := fset.Position(decl.End())

	// Build function ID — include receiver type to avoid collisions with same-named methods.
	funcID := fmt.Sprintf("func:%s", decl.Name.Name)
	receiver := ""
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		recvTypeName := types2str(decl.Recv.List[0].Type)
		funcID = fmt.Sprintf("func:%s.%s", recvTypeName, decl.Name.Name)
		// Recv FieldList positions include outer parens — strip them.
		recvText := extractText(fset, src, decl.Recv.Pos(), decl.Recv.End())
		if len(recvText) >= 2 && recvText[0] == '(' && recvText[len(recvText)-1] == ')' {
			receiver = strings.TrimSpace(recvText[1 : len(recvText)-1])
		} else {
			receiver = strings.TrimSpace(recvText)
		}
	}

	// Use raw source text for params and returns — preserves grouping, variadic, function types, etc.
	params := extractText(fset, src, decl.Type.Params.Pos(), decl.Type.Params.End())
	returns := ""
	if decl.Type.Results != nil {
		returns = extractText(fset, src, decl.Type.Results.Pos(), decl.Type.Results.End())
	}
	typeParams := ""
	if decl.Type.TypeParams != nil {
		typeParams = extractText(fset, src, decl.Type.TypeParams.Pos(), decl.Type.TypeParams.End())
	}

	// Extract the raw body text from source (between braces, contents only).
	bodyText := extractBodyText(fset, src, decl.Body)

	meta := map[string]string{
		"params":  params,
		"returns": returns,
	}
	if receiver != "" {
		meta["receiver"] = receiver
	}
	if typeParams != "" {
		meta["type_params"] = typeParams
	}
	// Compute complexity metrics.
	meta["cyclomatic"] = fmt.Sprintf("%d", CyclomaticComplexity(decl.Body))
	meta["cognitive"] = fmt.Sprintf("%d", CognitiveComplexity(decl.Body))

	nameStr := decl.Name.Name
	line, endLine := pos.Line, endPos.Line
	if err := g.AddNode(&graph.Node{
		ID:       funcID,
		Kind:     graph.KindFunction,
		Name:     nameStr,
		File:     fileID,
		Line:     line,
		EndLine:  endLine,
		Text:     bodyText,
		Metadata: meta,
	}); err != nil {
		// Node pre-created by addCallNode (local call seen before declaration) — update it.
		_ = g.UpdateNode(funcID, graph.NodePatch{
			Name:     &nameStr,
			Text:     &bodyText,
			File:     &fileID,
			Line:     &line,
			EndLine:  &endLine,
			Metadata: meta,
		})
	}
	_ = g.AddEdge(fileID, funcID, graph.EdgeContains)

	// Walk body for call expressions, creating structured call nodes.
	if decl.Body != nil {
		callIdx := 0
		ast.Inspect(decl.Body, func(n ast.Node) bool {
			if callExpr, ok := n.(*ast.CallExpr); ok {
				addCallNode(g, fset, src, funcID, callExpr, &callIdx)
			}
			return true
		})
	}
}
func ParseTree(g *graph.Graph, root string) (int, error) {
	var count int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", ".git", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		if perr := ParseFile(g, path); perr != nil {
			return nil
		}
		count++
		return nil
	})
	return count, err
}

// addCallNode creates a KindCall node plus shared symbol nodes for the callee.
// NOTE-001: fmt.Println(x) → call node referencing shared pkg:fmt and func:fmt.Println nodes.
func addCallNode(g *graph.Graph, fset *token.FileSet, src []byte, ownerID string, expr *ast.CallExpr, idx *int) {
	pos := fset.Position(expr.Pos())
	callText := extractText(fset, src, expr.Pos(), expr.End())
	callID := fmt.Sprintf("call:%s:%d", ownerID, *idx)
	*idx++

	_ = g.AddNode(&graph.Node{
		ID:   callID,
		Kind: graph.KindCall,
		Name: truncate(callText, 60),
		Text: callText,
		File: ownerID,
		Line: pos.Line,
	})
	_ = g.AddEdge(ownerID, callID, graph.EdgeContains)

	// Resolve the callee to shared symbol nodes.
	switch fn := expr.Fun.(type) {
	case *ast.SelectorExpr:
		// e.g. fmt.Println — receiver is an ident, method is Println.
		if recv, ok := fn.X.(*ast.Ident); ok {
			pkgNodeID := "pkg:" + recv.Name
			// Ensure the package node exists (may be external).
			_ = g.AddNode(&graph.Node{
				ID:   pkgNodeID,
				Kind: graph.KindPackage,
				Name: recv.Name,
				Metadata: map[string]string{"external": "true"},
			})
			_ = g.AddEdge(callID, pkgNodeID, graph.EdgeReferences)

			// Shared function node for pkg.Method — all call sites reference the same node.
			funcNodeID := fmt.Sprintf("func:%s.%s", recv.Name, fn.Sel.Name)
			_ = g.AddNode(&graph.Node{
				ID:   funcNodeID,
				Kind: graph.KindFunction,
				Name: fn.Sel.Name,
				Metadata: map[string]string{
					"package":  recv.Name,
					"external": "true",
				},
			})
			_ = g.AddEdge(callID, funcNodeID, graph.EdgeCallee)
		}

	case *ast.Ident:
		// e.g. make(x), len(x), or a local function call.
		funcNodeID := "func:" + fn.Name
		// Create if it doesn't already exist (builtins, local).
		_ = g.AddNode(&graph.Node{
			ID:   funcNodeID,
			Kind: graph.KindFunction,
			Name: fn.Name,
		})
		_ = g.AddEdge(callID, funcNodeID, graph.EdgeCallee)
	}

	// Argument nodes — each argument is a literal or expression node.
	for i, arg := range expr.Args {
		argText := extractText(fset, src, arg.Pos(), arg.End())
		argPos := fset.Position(arg.Pos())
		argID := fmt.Sprintf("%s:arg%d", callID, i)

		kind := graph.KindIdent
		if isLiteral(arg) {
			kind = graph.KindLiteral
		}

		_ = g.AddNode(&graph.Node{
			ID:   argID,
			Kind: kind,
			Name: argText,
			Text: argText,
			Line: argPos.Line,
			Metadata: map[string]string{
				"pos": fmt.Sprintf("%d", i),
			},
		})
		_ = g.AddEdge(callID, argID, graph.EdgeArg)
	}
}

func addGenDecl(g *graph.Graph, fset *token.FileSet, src []byte, fileID string, decl *ast.GenDecl) {
	isConst := decl.Tok == token.CONST
	isVar := decl.Tok == token.VAR

	// For grouped const/var blocks, store as a single raw block node to preserve
	// iota, formatting, and exact structure.
	if (isConst || isVar) && decl.Lparen.IsValid() {
		pos := fset.Position(decl.Pos())
		endPos := fset.Position(decl.End())
		keyword := "const"
		if isVar {
			keyword = "var"
		}
		blockText := extractText(fset, src, decl.Pos(), decl.End())
		blockID := fmt.Sprintf("%s_block:%d", keyword, pos.Line)
		_ = g.AddNode(&graph.Node{
			ID:      blockID,
			Kind:    graph.KindVariable,
			Name:    blockID,
			File:    fileID,
			Line:    pos.Line,
			EndLine: endPos.Line,
			Text:    blockText,
			Metadata: map[string]string{
				"raw":     "true",
				"keyword": keyword,
			},
		})
		_ = g.AddEdge(fileID, blockID, graph.EdgeContains)
		return
	}

	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.ImportSpec:
			pos := fset.Position(s.Pos())
			path := strings.Trim(s.Path.Value, `"`)
			importID := fmt.Sprintf("import:%s", path)
			meta := map[string]string{}
			if s.Name != nil {
				meta["alias"] = s.Name.Name
			}
			if decl.Lparen.IsValid() {
				meta["grouped"] = "true"
				if decl.Rparen.IsValid() {
					meta["block_end_line"] = fmt.Sprintf("%d", fset.Position(decl.Rparen).Line)
				}
			}
			_ = g.AddNode(&graph.Node{
				ID:       importID,
				Kind:     graph.KindImport,
				Name:     path,
				File:     fileID,
				Line:     pos.Line,
				Metadata: meta,
			})
			_ = g.AddEdge(fileID, importID, graph.EdgeContains)

		case *ast.TypeSpec:
			pos := fset.Position(s.Pos())
			endPos := fset.Position(s.End())
			typeID := fmt.Sprintf("type:%s", s.Name.Name)
			typeText := extractText(fset, src, s.Type.Pos(), s.Type.End())
			typeMeta := map[string]string{}
			if s.TypeParams != nil {
				typeMeta["type_params"] = extractText(fset, src, s.TypeParams.Pos(), s.TypeParams.End())
			}
			_ = g.AddNode(&graph.Node{
				ID:       typeID,
				Kind:     graph.KindType,
				Name:     s.Name.Name,
				File:     fileID,
				Line:     pos.Line,
				EndLine:  endPos.Line,
				Text:     typeText,
				Metadata: typeMeta,
			})
			_ = g.AddEdge(fileID, typeID, graph.EdgeContains)

		case *ast.ValueSpec:
			meta := map[string]string{}
			if isConst {
				meta["const"] = "true"
			}
			typeName := ""
			if s.Type != nil {
				typeName = extractText(fset, src, s.Type.Pos(), s.Type.End())
				meta["type"] = typeName
			}
			for i, name := range s.Names {
				pos := fset.Position(name.Pos())
				varID := fmt.Sprintf("var:%s", name.Name)
				valueText := ""
				if i < len(s.Values) {
					valueText = extractText(fset, src, s.Values[i].Pos(), s.Values[i].End())
				}
				_ = g.AddNode(&graph.Node{
					ID:       varID,
					Kind:     graph.KindVariable,
					Name:     name.Name,
					File:     fileID,
					Line:     pos.Line,
					Text:     valueText,
					Metadata: meta,
				})
				_ = g.AddEdge(fileID, varID, graph.EdgeContains)
			}
		}
	}
}

// extractBodyText returns the trimmed content inside a function body's braces.
func extractBodyText(fset *token.FileSet, src []byte, body *ast.BlockStmt) string {
	if body == nil || len(src) == 0 {
		return ""
	}
	// body.Pos() is '{', body.End()-1 is '}'.
	open := fset.Position(body.Lbrace)
	close := fset.Position(body.Rbrace)
	if open.Offset+1 >= close.Offset {
		return ""
	}
	inner := src[open.Offset+1 : close.Offset]
	// Split into lines and de-indent one tab level.
	// The inner slice always starts with '\n' (after '{') and ends before '}',
	// so the first and last elements after splitting are always empty — drop them
	// to avoid a spurious leading/trailing newline, but preserve internal blank lines.
	lines := strings.Split(string(inner), "\n")
	if len(lines) >= 2 {
		lines = lines[1 : len(lines)-1]
	}
	var out []string
	for _, l := range lines {
		out = append(out, strings.TrimPrefix(l, "\t"))
	}
	return strings.Join(out, "\n")
}

// extractText returns the raw source text for a node span.
func extractText(fset *token.FileSet, src []byte, from, to token.Pos) string {
	if len(src) == 0 {
		return ""
	}
	f := fset.Position(from)
	t := fset.Position(to)
	if f.Offset < 0 || t.Offset > len(src) {
		return ""
	}
	return string(src[f.Offset:t.Offset])
}

// types2str renders a type expression as a string.
func types2str(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + types2str(t.X)
	case *ast.SelectorExpr:
		return types2str(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + types2str(t.Elt)
	case *ast.MapType:
		return "map[" + types2str(t.Key) + "]" + types2str(t.Value)
	case *ast.InterfaceType:
		return "interface{}"
	default:
		return "any"
	}
}

// isLiteral reports whether an expression is a basic literal (string, int, etc.).
func isLiteral(expr ast.Expr) bool {
	_, ok := expr.(*ast.BasicLit)
	return ok
}

// buildDocTargetMap builds a map from comment group start position to the graph node ID
// of the declaration that the comment documents, using the AST's own doc-comment links.
func buildDocTargetMap(fset *token.FileSet, f *ast.File, fileID string) map[token.Pos]string {
	m := make(map[token.Pos]string)

	// Any comment group that starts before the `package` keyword → the file node.
	// This covers both attached doc comments (f.Doc) and loose pre-package comments
	// separated from `package` by a blank line (f.Doc == nil in that case).
	for _, cg := range f.Comments {
		if cg.Pos() < f.Package {
			m[cg.Pos()] = fileID
		}
	}

	// Each top-level declaration's doc comment → the declaration's node ID.
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Doc != nil {
				funcID := "func:" + d.Name.Name
				if d.Recv != nil && len(d.Recv.List) > 0 {
					recvTypeName := types2str(d.Recv.List[0].Type)
					funcID = fmt.Sprintf("func:%s.%s", recvTypeName, d.Name.Name)
				}
				m[d.Doc.Pos()] = funcID
			}
		case *ast.GenDecl:
			if d.Doc != nil {
				if (d.Tok == token.CONST || d.Tok == token.VAR) && d.Lparen.IsValid() {
					// Grouped block — map to block node ID.
					pos := fset.Position(d.Pos())
					keyword := "const"
					if d.Tok == token.VAR {
						keyword = "var"
					}
					blockID := fmt.Sprintf("%s_block:%d", keyword, pos.Line)
					m[d.Doc.Pos()] = blockID
				} else {
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							m[d.Doc.Pos()] = "type:" + s.Name.Name
						case *ast.ValueSpec:
							for _, name := range s.Names {
								m[d.Doc.Pos()] = "var:" + name.Name
							}
						}
					}
				}
			}
		}
	}
	return m
}

// findNearestCodeNode finds the code node in the file closest to and after commentEndLine.
func findNearestCodeNode(g *graph.Graph, fileID string, commentEndLine int) string {
	edges := g.EdgesFrom(fileID)
	bestID := ""
	bestLine := int(^uint(0) >> 1)

	for _, e := range edges {
		if e.Kind != graph.EdgeContains {
			continue
		}
		node, ok := g.GetNode(e.To)
		if !ok || node.Kind == graph.KindComment {
			continue
		}
		if node.Line >= commentEndLine && node.Line < bestLine {
			bestLine = node.Line
			bestID = node.ID
		}
	}
	return bestID
}

// extractDirectives scans a comment group for //go: directives and attaches
// them as metadata on the target node. Each directive becomes a metadata key
// (e.g. "go:embed", "go:build", "go:noinline") with the arguments as the value.
// Multiple directives of the same kind are space-joined.
func extractDirectives(g *graph.Graph, cg *ast.CommentGroup, targetID string) {
	if targetID == "" {
		return
	}
	for _, c := range cg.List {
		text := c.Text
		if !strings.HasPrefix(text, "//go:") {
			continue
		}
		// Strip the "//" prefix → "go:embed folder/*.hash"
		directive := strings.TrimPrefix(text, "//")
		// Split into name and args.
		name, args, _ := strings.Cut(directive, " ")
		args = strings.TrimSpace(args)

		targetNode, ok := g.GetNode(targetID)
		if !ok {
			continue
		}
		if targetNode.Metadata == nil {
			targetNode.Metadata = make(map[string]string)
		}
		if existing := targetNode.Metadata[name]; existing != "" {
			targetNode.Metadata[name] = existing + " " + args
		} else if args != "" {
			targetNode.Metadata[name] = args
		} else {
			targetNode.Metadata[name] = "true"
		}
	}
}

func truncate(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
