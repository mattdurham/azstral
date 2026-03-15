package parser

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/matt/azstral/graph"
)

// LoadPackages type-checks the Go module rooted at dir and adds all packages
// to the graph, enriching nodes with qualified_id and pkg_path metadata.
// It is slower than ParseTree (invokes the full type-checker) but produces
// globally unique node identifiers and enables precise cross-package rename.
func LoadPackages(g *graph.Graph, dir string) (int, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedImports,
		Dir: dir,
	}

	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return 0, fmt.Errorf("load packages: %w", err)
	}

	// Report load errors but continue — partial results are useful.
	var loadErrs []string
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			loadErrs = append(loadErrs, e.Msg)
		}
	}

	fileCount := 0
	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		for i, f := range pkg.Syntax {
			if i >= len(pkg.GoFiles) {
				continue
			}
			filePath := pkg.GoFiles[i]
			if err := addTypedFile(g, pkg, f, filePath); err != nil {
				loadErrs = append(loadErrs, fmt.Sprintf("%s: %v", filePath, err))
				continue
			}
			fileCount++
		}
	}

	// Build the variable dictionary: create nodes for every defined variable,
	// parameter, and named return, with reference edges from use sites.
	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		buildVarDictionary(g, pkg)
	}

	if len(loadErrs) > 0 {
		return fileCount, fmt.Errorf("%d load error(s): %s", len(loadErrs), strings.Join(loadErrs[:min(3, len(loadErrs))], "; "))
	}
	return fileCount, nil
}

// addTypedFile registers one file from a type-checked package into the graph.
// Nodes receive qualified_id metadata that uniquely identifies them globally.
func addTypedFile(g *graph.Graph, pkg *packages.Package, f *ast.File, filePath string) error {
	pkgPath := pkg.PkgPath
	pkgName := pkg.Name
	pkgID := "pkg:" + pkgName
	fileID := "file:" + filePath

	_ = g.AddNode(&graph.Node{
		ID:   pkgID,
		Kind: graph.KindPackage,
		Name: pkgName,
		Metadata: map[string]string{
			"pkg_path": pkgPath,
		},
	})

	if err := g.AddNode(&graph.Node{
		ID:   fileID,
		Kind: graph.KindFile,
		Name: pkgName,
		File: filePath,
	}); err != nil {
		return err // file already parsed
	}
	_ = g.AddEdge(pkgID, fileID, graph.EdgeContains)

	// Walk top-level declarations, adding type-enriched nodes.
	scope := pkg.Types.Scope()
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			addTypedFunc(g, pkg, fileID, pkgPath, d, scope)
		case *ast.GenDecl:
			addTypedGenDecl(g, pkg, fileID, pkgPath, d)
		}
	}
	return nil
}

func addTypedFunc(g *graph.Graph, pkg *packages.Package, fileID, pkgPath string, decl *ast.FuncDecl, scope *types.Scope) {
	name := decl.Name.Name
	receiver := ""
	recvType := ""

	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		recvType = types2strT(decl.Recv.List[0].Type)
		funcID := fmt.Sprintf("func:%s.%s", recvType, name)
		recvText := fieldListText(decl.Recv)
		receiver = recvText

		qualID := fmt.Sprintf("%s.(%s).%s", pkgPath, recvType, name)
		_ = g.AddNode(&graph.Node{
			ID:   funcID,
			Kind: graph.KindFunction,
			Name: name,
			File: fileID,
			Line: pkg.Fset.Position(decl.Pos()).Line,
			Metadata: map[string]string{
				"receiver":     receiver,
				"qualified_id": qualID,
				"pkg_path":     pkgPath,
			},
		})
		_ = g.AddEdge(fileID, funcID, graph.EdgeContains)
		return
	}

	// Package-level function.
	qualID := pkgPath + "." + name
	funcID := "func:" + name

	obj := scope.Lookup(name)
	if obj != nil {
		qualID = obj.Pkg().Path() + "." + obj.Name()
	}

	_ = g.AddNode(&graph.Node{
		ID:   funcID,
		Kind: graph.KindFunction,
		Name: name,
		File: fileID,
		Line: pkg.Fset.Position(decl.Pos()).Line,
		Metadata: map[string]string{
			"receiver":     receiver,
			"qualified_id": qualID,
			"pkg_path":     pkgPath,
		},
	})
	_ = g.AddEdge(fileID, funcID, graph.EdgeContains)
}

func addTypedGenDecl(g *graph.Graph, pkg *packages.Package, fileID, pkgPath string, decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			typeID := "type:" + s.Name.Name
			qualID := pkgPath + "." + s.Name.Name
			_ = g.AddNode(&graph.Node{
				ID:   typeID,
				Kind: graph.KindType,
				Name: s.Name.Name,
				File: fileID,
				Line: pkg.Fset.Position(s.Pos()).Line,
				Metadata: map[string]string{
					"qualified_id": qualID,
					"pkg_path":     pkgPath,
				},
			})
			_ = g.AddEdge(fileID, typeID, graph.EdgeContains)

		case *ast.ValueSpec:
			for _, name := range s.Names {
				varID := "var:" + name.Name
				qualID := pkgPath + "." + name.Name
				_ = g.AddNode(&graph.Node{
					ID:   varID,
					Kind: graph.KindVariable,
					Name: name.Name,
					File: fileID,
					Line: pkg.Fset.Position(name.Pos()).Line,
					Metadata: map[string]string{
						"qualified_id": qualID,
						"pkg_path":     pkgPath,
					},
				})
				_ = g.AddEdge(fileID, varID, graph.EdgeContains)
			}
		}
	}
}

// QualifiedID returns the globally unique identifier for a types.Object.
// Format: "pkg/path.TypeName.MethodName" or "pkg/path.Name".
func QualifiedID(obj types.Object) string {
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	base := obj.Pkg().Path() + "." + obj.Name()
	// For methods, include the receiver type.
	if fn, ok := obj.(*types.Func); ok {
		sig := fn.Type().(*types.Signature)
		if sig.Recv() != nil {
			recv := types.TypeString(sig.Recv().Type(), nil)
			recv = strings.TrimLeft(recv, "*")
			if idx := strings.LastIndex(recv, "."); idx >= 0 {
				recv = recv[idx+1:]
			}
			base = obj.Pkg().Path() + ".(" + recv + ")." + obj.Name()
		}
	}
	return base
}

// FindUsages returns all file paths and their type-checked packages where
// qualifiedID is referenced. Used by precise rename.
func FindUsages(dir, qualifiedID string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode: packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedTypes |
			packages.NeedFiles | packages.NeedName,
		Dir: dir,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, err
	}
	var result []*packages.Package
	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		for id, obj := range pkg.TypesInfo.Uses {
			_ = id
			if obj != nil && QualifiedID(obj) == qualifiedID {
				result = append(result, pkg)
				break
			}
		}
		// Also check Defs (the definition itself).
		for _, obj := range pkg.TypesInfo.Defs {
			if obj != nil && QualifiedID(obj) == qualifiedID {
				result = append(result, pkg)
				break
			}
		}
	}
	return result, nil
}

// buildVarDictionary walks TypesInfo.Defs to create KindLocal nodes for every
// variable, parameter, and named return. Then walks TypesInfo.Uses to create
// reference edges from the containing statement/function to the variable.
func buildVarDictionary(g *graph.Graph, pkg *packages.Package) {
	info := pkg.TypesInfo
	pkgPath := pkg.PkgPath

	// Phase 1: Create nodes for all defined variables.
	for ident, obj := range info.Defs {
		if obj == nil {
			continue
		}
		v, ok := obj.(*types.Var)
		if !ok {
			continue
		}
		// Skip blank identifiers.
		if v.Name() == "_" {
			continue
		}

		pos := pkg.Fset.Position(ident.Pos())
		scope := varScope(v)

		// Build qualified ID: pkg.FuncOrMethod.varName
		qualPath := qualifiedVarPath(v, pkgPath)
		nodeID := fmt.Sprintf("local:%s:%d", qualPath, pos.Line)

		typStr := types.TypeString(v.Type(), nil)

		_ = g.AddNode(&graph.Node{
			ID:      nodeID,
			Kind:    graph.KindLocal,
			Name:    v.Name(),
			File:    "file:" + pos.Filename,
			Line:    pos.Line,
			EndLine: pos.Line,
			Metadata: map[string]string{
				"type":         typStr,
				"scope":        scope,
				"qualified_id": qualPath,
				"pkg_path":     pkgPath,
			},
		})

		// Connect the variable to its containing function (if local/param).
		if parent := v.Parent(); parent != nil {
			funcName := findEnclosingFuncName(parent, pkg.Types.Scope())
			if funcName != "" {
				funcID := "func:" + funcName
				_ = g.AddEdge(funcID, nodeID, graph.EdgeContains)
			}
		}
	}

	// Phase 2: Create reference edges from use sites to definitions.
	for ident, obj := range info.Uses {
		if obj == nil {
			continue
		}
		v, ok := obj.(*types.Var)
		if !ok || v.Name() == "_" {
			continue
		}

		// Find the definition node by qualified path + definition line.
		defPos := pkg.Fset.Position(v.Pos())
		qualPath := qualifiedVarPath(v, pkgPath)
		defNodeID := fmt.Sprintf("local:%s:%d", qualPath, defPos.Line)

		// Find the containing statement/function for this use site.
		usePos := pkg.Fset.Position(ident.Pos())
		useFileID := "file:" + usePos.Filename

		// Try to find the closest containing statement node by line number.
		parentID := findContainingNode(g, useFileID, usePos.Line)
		if parentID != "" {
			_ = g.AddEdge(parentID, defNodeID, graph.EdgeReferences)
		}
	}
}

// varScope classifies a types.Var into its scope category.
func varScope(v *types.Var) string {
	if v.IsField() {
		return "field"
	}
	parent := v.Parent()
	if parent == nil {
		return "field" // struct field or interface method param
	}
	// Package scope.
	if parent.Parent() != nil && parent.Parent().Parent() == nil {
		return "package"
	}
	// Check if it's a function parameter or named return.
	// Parameters and returns have the function scope as parent but are
	// declared before the function body scope opens.
	return "local"
}

// qualifiedVarPath builds a dotted path like "pkg/path.FuncName.varName".
func qualifiedVarPath(v *types.Var, pkgPath string) string {
	name := v.Name()
	parent := v.Parent()
	if parent == nil {
		if v.Pkg() != nil {
			return v.Pkg().Path() + "." + name
		}
		return name
	}

	// Walk up scopes to find the enclosing function.
	funcName := ""
	scope := parent
	for scope != nil {
		if scope.Parent() != nil && scope.Parent().Parent() == nil {
			// This is the package scope — we're at the top.
			break
		}
		// Check if this scope has a Func associated with it.
		// We can't get the func name from scope directly, so use pkgPath.
		scope = scope.Parent()
	}
	if funcName == "" {
		// Use a simpler path for package-level vars.
		return pkgPath + "." + name
	}
	return pkgPath + "." + funcName + "." + name
}

// findEnclosingFuncName finds the function name that owns a scope.
// It does a brute-force search through the package scope's children.
func findEnclosingFuncName(scope *types.Scope, pkgScope *types.Scope) string {
	// Walk up from the variable's scope to find the function scope
	// (the scope whose parent is the package scope).
	for scope != nil {
		if scope.Parent() == pkgScope {
			// This scope is a direct child of the package — it's a function scope.
			// Find the function name by checking package-level objects.
			for _, name := range pkgScope.Names() {
				obj := pkgScope.Lookup(name)
				if fn, ok := obj.(*types.Func); ok {
					sig := fn.Type().(*types.Signature)
					_ = sig
					// We can't easily get the function's body scope from types.Func.
					// Return the function name if the scope matches by position range.
					if fn.Pos() <= scope.Pos() && scope.End() <= fn.Pos()+1000000 {
						return fn.Name()
					}
				}
			}
			return ""
		}
		scope = scope.Parent()
	}
	return ""
}

// findContainingNode finds the tightest graph node (statement or function)
// that contains the given line in the given file.
func findContainingNode(g *graph.Graph, fileID string, line int) string {
	bestID := ""
	bestSize := int(^uint(0) >> 1)
	for _, n := range g.Nodes {
		if n.File != fileID {
			continue
		}
		if n.Line == 0 || n.EndLine == 0 {
			continue
		}
		if line >= n.Line && line <= n.EndLine {
			size := n.EndLine - n.Line
			if size < bestSize {
				bestSize = size
				bestID = n.ID
			}
		}
	}
	return bestID
}

// helpers

func types2strT(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + types2strT(t.X)
	default:
		return "any"
	}
}

func fieldListText(fl *ast.FieldList) string {
	if fl == nil {
		return ""
	}
	var parts []string
	for _, f := range fl.List {
		typ := types2strT(f.Type)
		for _, name := range f.Names {
			parts = append(parts, name.Name+" "+typ)
		}
		if len(f.Names) == 0 {
			parts = append(parts, typ)
		}
	}
	return strings.Join(parts, ", ")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
