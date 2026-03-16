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

	// Range loops now use KindForRange sub-kind.
	rangeNodes := g.NodesByKind(graph.KindForRange)
	if len(rangeNodes) < 1 {
		t.Errorf("expected ≥1 for.range nodes, got %d", len(rangeNodes))
	}
	var rangeFound bool
	for _, n := range rangeNodes {
		if n.Name == "items" || n.Metadata["over"] == "items" {
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

	// Send statement.
	if count(graph.KindSend) < 2 {
		t.Errorf("expected ≥2 send nodes, got %d", count(graph.KindSend))
	}
	for _, n := range g.NodesByKind(graph.KindSend) {
		if n.Metadata["ch"] == "" {
			t.Errorf("send node missing ch metadata: %+v", n)
		}
	}

	// Branch (continue) now uses KindBranchContinue.
	if count(graph.KindBranchContinue) < 1 {
		t.Errorf("expected ≥1 branch.continue node, got %d", count(graph.KindBranchContinue))
	}

	// Go statement.
	if count(graph.KindGo) < 1 {
		t.Errorf("expected ≥1 go node, got %d", count(graph.KindGo))
	}

	// Short declaration now uses KindAssignDecl.
	if count(graph.KindAssignDecl) < 1 {
		t.Errorf("expected ≥1 assign.decl node, got %d", count(graph.KindAssignDecl))
	}
	if len(g.NodesByKind(graph.KindAssignDecl)) > 0 {
		n := g.NodesByKind(graph.KindAssignDecl)[0]
		if n.Metadata["op"] != ":=" {
			t.Errorf("assign.decl op = %q, want :=", n.Metadata["op"])
		}
	}

	// Containment: range loop should be a child of func:main.process.
	funcNode, ok := g.GetNode("func:main.process")
	if !ok {
		t.Fatal("func:main.process not found")
	}
	children := g.Children(funcNode.ID)
	var hasFor bool
	for _, c := range children {
		if c.Kind == graph.KindForRange || c.Kind == graph.KindForLoop ||
			c.Kind == graph.KindForCond || c.Kind == graph.KindForBare || c.Kind == graph.KindFor {
			hasFor = true
		}
	}
	if !hasFor {
		t.Error("for node not a direct child of func:process")
	}

	// Nested: if should be inside the range loop.
	for _, forNode := range rangeNodes {
		nested := g.Children(forNode.ID)
		var hasIf bool
		for _, c := range nested {
			if c.Kind == graph.KindIf {
				hasIf = true
			}
		}
		if !hasIf && forNode.Metadata["over"] == "items" {
			t.Error("if not nested inside range loop over items")
		}
	}

	t.Logf("for.range=%d for.cond=%d for.loop=%d for.bare=%d if=%d switch=%d select=%d return=%d defer=%d go=%d assign.decl=%d assign.set=%d send=%d branch.break=%d branch.continue=%d",
		count(graph.KindForRange), count(graph.KindForCond), count(graph.KindForLoop), count(graph.KindForBare),
		count(graph.KindIf), count(graph.KindSwitch), count(graph.KindSelect), count(graph.KindReturn),
		count(graph.KindDefer), count(graph.KindGo),
		count(graph.KindAssignDecl), count(graph.KindAssignSet),
		count(graph.KindSend), count(graph.KindBranchBreak), count(graph.KindBranchContinue))
}
