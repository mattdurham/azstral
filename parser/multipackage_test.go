package parser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/matt/azstral/graph"
)

// TestParseTree_CrossPackageCollision verifies that when two packages define
// a symbol with the same name (e.g. func:New), the second package's file does
// not gain a contains edge to the first package's symbol node.
func TestParseTree_CrossPackageCollision(t *testing.T) {
	root := t.TempDir()

	// Package alpha defines func New and type Config.
	alphaDir := filepath.Join(root, "alpha")
	os.MkdirAll(alphaDir, 0o755)
	writeTestFile(t, filepath.Join(alphaDir, "alpha.go"), `package alpha

type Config struct{ X int }

func New() *Config { return &Config{} }
`)

	// Package beta also defines func New and type Config.
	betaDir := filepath.Join(root, "beta")
	os.MkdirAll(betaDir, 0o755)
	writeTestFile(t, filepath.Join(betaDir, "beta.go"), `package beta

type Config struct{ Y string }

func New() *Config { return &Config{} }
`)

	g := graph.New()
	_, err := ParseTree(g, root)
	if err != nil {
		t.Fatal(err)
	}

	alphaFile := "file:" + filepath.Join(alphaDir, "alpha.go")
	betaFile := "file:" + filepath.Join(betaDir, "beta.go")

	// Collect children of each file.
	alphaChildren := g.Children(alphaFile)
	betaChildren := g.Children(betaFile)

	// Verify no child of beta's file points to alpha's symbols (or vice versa).
	for _, n := range betaChildren {
		if n.File != "" && n.File != betaFile {
			t.Errorf("beta file has child %s with File=%s (should be %s)",
				n.ID, n.File, betaFile)
		}
	}
	for _, n := range alphaChildren {
		if n.File != "" && n.File != alphaFile {
			t.Errorf("alpha file has child %s with File=%s (should be %s)",
				n.ID, n.File, alphaFile)
		}
	}

	// The primary invariant: no file should contain a child whose File field
	// points to a different file. Name collisions are dropped (first-parse wins)
	// rather than causing cross-file contamination.
	// NOTE: when two packages share a name like func:New, the second package
	// parsed loses that symbol. This is a known limitation until package-qualified
	// IDs are implemented.
	t.Logf("alpha children: %d, beta children: %d", len(alphaChildren), len(betaChildren))
}

// TestParseTree_BlockIDUniqueness verifies that grouped const/var blocks from
// different files do not collide even if they appear on the same line number.
func TestParseTree_BlockIDUniqueness(t *testing.T) {
	root := t.TempDir()

	aDir := filepath.Join(root, "a")
	bDir := filepath.Join(root, "b")
	os.MkdirAll(aDir, 0o755)
	os.MkdirAll(bDir, 0o755)

	// Both files have a const block starting at line 3.
	src := "package p\n\nconst (\n\tX = 1\n\tY = 2\n)\n"
	writeTestFile(t, filepath.Join(aDir, "a.go"), src)
	writeTestFile(t, filepath.Join(bDir, "b.go"), src)

	g := graph.New()
	if _, err := ParseTree(g, root); err != nil {
		t.Fatal(err)
	}

	aFile := "file:" + filepath.Join(aDir, "a.go")
	bFile := "file:" + filepath.Join(bDir, "b.go")

	// Each file should have exactly one variable (const block) child.
	aVars := 0
	for _, c := range g.Children(aFile) {
		if c.Kind == graph.KindVariable {
			aVars++
			if c.File != aFile {
				t.Errorf("a's const block has wrong File: %s", c.File)
			}
		}
	}
	bVars := 0
	for _, c := range g.Children(bFile) {
		if c.Kind == graph.KindVariable {
			bVars++
			if c.File != bFile {
				t.Errorf("b's const block has wrong File: %s", c.File)
			}
		}
	}

	if aVars != 1 {
		t.Errorf("a has %d variable children, want 1", aVars)
	}
	if bVars != 1 {
		t.Errorf("b has %d variable children, want 1", bVars)
	}
}

// TestParseTree_CommentIDUniqueness verifies that comment nodes from files
// with the same basename don't collide.
func TestParseTree_CommentIDUniqueness(t *testing.T) {
	root := t.TempDir()

	aDir := filepath.Join(root, "a")
	bDir := filepath.Join(root, "b")
	os.MkdirAll(aDir, 0o755)
	os.MkdirAll(bDir, 0o755)

	// Both files named util.go with a comment on line 1.
	src := "// Package comment.\npackage p\n\nfunc F() {}\n"
	writeTestFile(t, filepath.Join(aDir, "util.go"), src)
	writeTestFile(t, filepath.Join(bDir, "util.go"), src)

	g := graph.New()
	if _, err := ParseTree(g, root); err != nil {
		t.Fatal(err)
	}

	// Should have two separate comment nodes, one per file.
	comments := g.NodesByKind(graph.KindComment)
	if len(comments) < 2 {
		t.Errorf("expected at least 2 comment nodes (one per file), got %d", len(comments))
	}
}

// TestParseTree_ImportAlias verifies that two files importing the same package
// with different aliases each get their own import node with the correct alias.
func TestParseTree_ImportAlias(t *testing.T) {
	root := t.TempDir()

	aDir := filepath.Join(root, "a")
	bDir := filepath.Join(root, "b")
	os.MkdirAll(aDir, 0o755)
	os.MkdirAll(bDir, 0o755)

	// File A imports encoding/json with alias "j".
	writeTestFile(t, filepath.Join(aDir, "a.go"), `package a

import j "encoding/json"

func F() { j.Marshal(nil) }
`)

	// File B imports encoding/json without alias.
	writeTestFile(t, filepath.Join(bDir, "b.go"), `package b

import "encoding/json"

func G() { json.Marshal(nil) }
`)

	g := graph.New()
	if _, err := ParseTree(g, root); err != nil {
		t.Fatal(err)
	}

	fileA := "file:" + filepath.Join(aDir, "a.go")
	fileB := "file:" + filepath.Join(bDir, "b.go")

	// Find the import node for encoding/json in each file.
	aliasA, aliasB := "", "NOT_FOUND"
	for _, n := range g.NodesByKind(graph.KindImport) {
		if n.File != fileA && n.File != fileB {
			continue
		}
		if n.Name != "encoding/json" {
			continue
		}
		if n.File == fileA {
			aliasA = n.Metadata["alias"]
		}
		if n.File == fileB {
			aliasB = n.Metadata["alias"]
		}
	}

	if aliasA != "j" {
		t.Errorf("file A import alias = %q, want %q", aliasA, "j")
	}
	if aliasB != "" {
		t.Errorf("file B import alias = %q, want empty (no alias)", aliasB)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
