package parser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/matt/azstral/graph"
)

const stmtTestSrc = `package main

import "fmt"

func process(items []string, ch chan string) {
	for _, item := range items {
		if item == "" {
			continue
		}
		fmt.Println(item)
	}

	for i := 0; i < 10; i++ {
		select {
		case ch <- "msg":
		default:
		}
	}

	switch len(items) {
	case 0:
		return
	case 1:
		defer fmt.Println("done")
	}

	x := 42
	_ = x
	ch <- "final"
}

func spawn(done chan struct{}) {
	go func() {
		done <- struct{}{}
	}()
}
`

func TestStatementNodes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	os.WriteFile(path, []byte(stmtTestSrc), 0o644)

	g := graph.New()
	if err := ParseFile(g, path); err != nil {
		t.Fatal(err)
	}

	count := func(kind graph.NodeKind) int {
		return len(g.NodesByKind(kind))
	}

	// Range loop (for _, item := range items).
	forNodes := g.NodesByKind(graph.KindFor)
	if len(forNodes) < 2 {
		t.Errorf("expected ≥2 for nodes, got %d", len(forNodes))
	}
	var rangeFound bool
	for _, n := range forNodes {
		if n.Metadata["range"] == "true" && n.Metadata["over"] == "items" {
			rangeFound = true
		}
	}
	if !rangeFound {
		t.Error("range loop over 'items' not found")
	}

	// If statement.
	if count(graph.KindIf) < 1 {
		t.Errorf("expected ≥1 if node, got %d", count(graph.KindIf))
	}

	// Select statement.
	if count(graph.KindSelect) < 1 {
		t.Errorf("expected ≥1 select node, got %d", count(graph.KindSelect))
	}

	// Switch statement.
	if count(graph.KindSwitch) < 1 {
		t.Errorf("expected ≥1 switch node, got %d", count(graph.KindSwitch))
	}

	// Return statement.
	if count(graph.KindReturn) < 1 {
		t.Errorf("expected ≥1 return node, got %d", count(graph.KindReturn))
	}

	// Defer statement.
	if count(graph.KindDefer) < 1 {
		t.Errorf("expected ≥1 defer node, got %d", count(graph.KindDefer))
	}

	// Send statement (ch <- "final" and done <- struct{}{}).
	if count(graph.KindSend) < 2 {
		t.Errorf("expected ≥2 send nodes, got %d", count(graph.KindSend))
	}
	for _, n := range g.NodesByKind(graph.KindSend) {
		if n.Metadata["ch"] == "" {
			t.Errorf("send node missing ch metadata: %+v", n)
		}
	}

	// Branch (continue).
	if count(graph.KindBranch) < 1 {
		t.Errorf("expected ≥1 branch node, got %d", count(graph.KindBranch))
	}

	// Go statement.
	if count(graph.KindGo) < 1 {
		t.Errorf("expected ≥1 go node, got %d", count(graph.KindGo))
	}

	// Assign statement (x := 42).
	if count(graph.KindAssign) < 1 {
		t.Errorf("expected ≥1 assign node, got %d", count(graph.KindAssign))
	}
	var shortDecl bool
	for _, n := range g.NodesByKind(graph.KindAssign) {
		if n.Metadata["op"] == ":=" {
			shortDecl = true
		}
	}
	if !shortDecl {
		t.Error("no := assignment found")
	}

	// Containment: range loop should be a child of func:process.
	funcNode, ok := g.GetNode("func:process")
	if !ok {
		t.Fatal("func:process not found")
	}
	children := g.Children(funcNode.ID)
	var hasFor bool
	for _, c := range children {
		if c.Kind == graph.KindFor {
			hasFor = true
		}
	}
	if !hasFor {
		t.Error("for node not a direct child of func:process")
	}

	// Nested: if should be inside the range loop.
	for _, forNode := range forNodes {
		if forNode.Metadata["range"] == "true" {
			nested := g.Children(forNode.ID)
			var hasIf bool
			for _, c := range nested {
				if c.Kind == graph.KindIf {
					hasIf = true
				}
			}
			if !hasIf {
				t.Error("if not nested inside range loop")
			}
		}
	}

	t.Logf("for=%d if=%d switch=%d select=%d return=%d defer=%d go=%d assign=%d send=%d branch=%d",
		count(graph.KindFor), count(graph.KindIf), count(graph.KindSwitch),
		count(graph.KindSelect), count(graph.KindReturn), count(graph.KindDefer),
		count(graph.KindGo), count(graph.KindAssign), count(graph.KindSend),
		count(graph.KindBranch))
}
