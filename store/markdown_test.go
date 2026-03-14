package store

import (
	"os"
	"path/filepath"
	"testing"
)

func setupMarkdownTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Root-level SPECS.md.
	writeFile(t, filepath.Join(root, "SPECS.md"), `# Specs

## SPEC-001: Root spec
The system shall do X.

## SPEC-002: Another root spec
The system shall do Y.
`)

	// Root-level NOTES.md.
	writeFile(t, filepath.Join(root, "NOTES.md"), `# Notes

## NOTE-001
The graph is the core abstraction.
`)

	// Subdirectory with overriding SPEC-001.
	sub := filepath.Join(root, "parser")
	os.MkdirAll(sub, 0o755)
	writeFile(t, filepath.Join(sub, "SPECS.md"), `# Specs

## SPEC-001: Parser-specific spec
The parser shall do Z.

## SPEC-003: Parser-only spec
Only exists at parser level.
`)

	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMarkdownOpenAndGet(t *testing.T) {
	root := setupMarkdownTree(t)
	ms, err := OpenMarkdown(root)
	if err != nil {
		t.Fatal(err)
	}

	// Root-level GetSpec should return root spec.
	sp, err := ms.GetSpec("SPEC-001")
	if err != nil {
		t.Fatal(err)
	}
	if sp.Title != "Root spec" {
		t.Errorf("expected root spec title, got %q", sp.Title)
	}
}

func TestMarkdownResolveHierarchy(t *testing.T) {
	root := setupMarkdownTree(t)
	ms, err := OpenMarkdown(root)
	if err != nil {
		t.Fatal(err)
	}

	parserDir := filepath.Join(root, "parser")

	// Resolve from parser directory should find parser-level SPEC-001.
	sp, err := ms.ResolveSpec("SPEC-001", filepath.Join(parserDir, "parser.go"))
	if err != nil {
		t.Fatal(err)
	}
	if sp.Title != "Parser-specific spec" {
		t.Errorf("expected parser spec, got %q", sp.Title)
	}

	// Resolve from root should find root SPEC-001.
	sp, err = ms.ResolveSpec("SPEC-001", filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if sp.Title != "Root spec" {
		t.Errorf("expected root spec, got %q", sp.Title)
	}

	// SPEC-002 only exists at root — should resolve from anywhere.
	sp, err = ms.ResolveSpec("SPEC-002", filepath.Join(parserDir, "parser.go"))
	if err != nil {
		t.Fatal(err)
	}
	if sp.Title != "Another root spec" {
		t.Errorf("expected root SPEC-002, got %q", sp.Title)
	}

	// SPEC-003 only exists at parser level — shouldn't resolve from root.
	sp, err = ms.ResolveSpec("SPEC-003", filepath.Join(parserDir, "parser.go"))
	if err != nil {
		t.Fatal(err)
	}
	if sp.Title != "Parser-only spec" {
		t.Errorf("expected parser SPEC-003, got %q", sp.Title)
	}
}

func TestMarkdownListSpecs(t *testing.T) {
	root := setupMarkdownTree(t)
	ms, err := OpenMarkdown(root)
	if err != nil {
		t.Fatal(err)
	}

	// List all SPECs.
	specs, err := ms.ListSpecs("SPEC", "")
	if err != nil {
		t.Fatal(err)
	}
	// SPEC-001 (root), SPEC-001 (parser), SPEC-002 (root), SPEC-003 (parser).
	if len(specs) != 4 {
		t.Errorf("expected 4 specs, got %d", len(specs))
	}

	// List NOTEs.
	notes, err := ms.ListSpecs("NOTE", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Errorf("expected 1 note, got %d", len(notes))
	}
}

func TestMarkdownCreateSpec(t *testing.T) {
	root := setupMarkdownTree(t)
	ms, err := OpenMarkdown(root)
	if err != nil {
		t.Fatal(err)
	}

	// Create a new spec at root.
	err = ms.CreateSpec(&Spec{
		ID:    "SPEC-010",
		Kind:  "SPEC",
		Title: "New spec",
		Body:  "This is a newly created spec.",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Should be retrievable.
	sp, err := ms.GetSpec("SPEC-010")
	if err != nil {
		t.Fatal(err)
	}
	if sp.Title != "New spec" {
		t.Errorf("expected 'New spec', got %q", sp.Title)
	}

	// Create a spec in a subdirectory.
	err = ms.CreateSpec(&Spec{
		ID:        "SPEC-020",
		Kind:      "SPEC",
		Namespace: "parser",
		Title:     "Parser new spec",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Should be resolvable from parser dir.
	sp, err = ms.ResolveSpec("SPEC-020", filepath.Join(root, "parser", "parser.go"))
	if err != nil {
		t.Fatal(err)
	}
	if sp.Title != "Parser new spec" {
		t.Errorf("expected 'Parser new spec', got %q", sp.Title)
	}
}

func TestMarkdownNamespace(t *testing.T) {
	root := setupMarkdownTree(t)
	ms, err := OpenMarkdown(root)
	if err != nil {
		t.Fatal(err)
	}

	// Root specs have empty namespace.
	sp, _ := ms.GetSpec("SPEC-002")
	if sp.Namespace != "" {
		t.Errorf("root spec namespace should be empty, got %q", sp.Namespace)
	}

	// Parser specs have "parser" namespace.
	sp, _ = ms.ResolveSpec("SPEC-003", filepath.Join(root, "parser", "x.go"))
	if sp.Namespace != "parser" {
		t.Errorf("parser spec namespace should be 'parser', got %q", sp.Namespace)
	}
}
