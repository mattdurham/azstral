package edit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

func AddImport(filePath, importPath, alias string) error {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", filePath, err)
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", filePath, err)
	}

	// Check if import already exists.
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == importPath {
			return nil // already present
		}
	}

	// Build the new import spec line.
	var spec string
	if alias != "" {
		spec = fmt.Sprintf("%s %q", alias, importPath)
	} else {
		spec = fmt.Sprintf("%q", importPath)
	}

	// If there's an existing import block, add to it.
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		if gd.Lparen.IsValid() {
			// Insert before the closing paren.
			closeOff := fset.Position(gd.Rparen).Offset
			line := "\t" + spec + "\n"
			out := append(src[:closeOff], append([]byte(line), src[closeOff:]...)...)
			return os.WriteFile(filePath, out, 0o644)
		}
		// Single-import without parens: convert to grouped.
		start := fset.Position(gd.Pos()).Offset
		end := fset.Position(gd.End()).Offset
		existing := strings.TrimSpace(string(src[start:end]))
		// existing is like: import "fmt"
		existing = strings.TrimPrefix(existing, "import")
		existing = strings.TrimSpace(existing)
		newBlock := fmt.Sprintf("import (\n\t%s\n\t%s\n)", strings.TrimSpace(existing), spec)
		out := append(src[:start], append([]byte(newBlock), src[end:]...)...)
		return os.WriteFile(filePath, out, 0o644)
	}

	// No import block yet — add one after the package declaration.
	pkgEnd := fset.Position(f.Name.End()).Offset
	// Find end of the package line.
	for pkgEnd < len(src) && src[pkgEnd] != '\n' {
		pkgEnd++
	}
	newBlock := fmt.Sprintf("\n\nimport %q", importPath)
	if alias != "" {
		newBlock = fmt.Sprintf("\n\nimport %s %q", alias, importPath)
	}
	out := append(src[:pkgEnd], append([]byte(newBlock), src[pkgEnd:]...)...)
	return os.WriteFile(filePath, out, 0o644)
}

func RemoveImport(filePath, importPath string) error {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", filePath, err)
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", filePath, err)
	}

	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		for _, spec := range gd.Specs {
			imp, ok := spec.(*ast.ImportSpec)
			if !ok {
				continue
			}
			path := strings.Trim(imp.Path.Value, `"`)
			if path != importPath {
				continue
			}
			// Found. Remove this spec.
			start := fset.Position(imp.Pos()).Offset
			end := fset.Position(imp.End()).Offset
			// Extend to include the full line (including newline and leading whitespace).
			lineStart := start
			for lineStart > 0 && src[lineStart-1] != '\n' {
				lineStart--
			}
			lineEnd := end
			for lineEnd < len(src) && src[lineEnd] != '\n' {
				lineEnd++
			}
			if lineEnd < len(src) {
				lineEnd++ // include the newline
			}
			out := append(src[:lineStart], src[lineEnd:]...)

			// If the block is now empty (only parens left), remove the whole block.
			if len(gd.Specs) == 1 {
				blockStart := fset.Position(gd.Pos()).Offset
				blockEnd := fset.Position(gd.End()).Offset
				// Extend blockStart backward to eat the preceding blank line.
				for blockStart > 0 && src[blockStart-1] == '\n' {
					blockStart--
				}
				out = append(src[:blockStart], src[blockEnd:]...)
			}
			return os.WriteFile(filePath, out, 0o644)
		}
	}
	return fmt.Errorf("import %q not found in %s", importPath, filePath)
}
