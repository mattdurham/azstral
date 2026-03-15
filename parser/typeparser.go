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
