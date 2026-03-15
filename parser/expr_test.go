package parser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/matt/azstral/graph"
)

const exprTestSrc = `package main

func calc(items []string) string {
	x := 1 + 2
	if x > 0 {
		_ = x
	}
	for i := 0; i < len(items); i++ {
	}
	return items[0]
}

func send(ch chan int, v int) {
	ch <- v + 1
}
`

func TestExpressionNodes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	os.WriteFile(path, []byte(exprTestSrc), 0o644)

	g := graph.New()
	if err := ParseFile(g, path); err != nil {
		t.Fatal(err)
	}

	count := func(kind graph.NodeKind) int {
		return len(g.NodesByKind(kind))
	}

	// --- Binary expression nodes ---

	// x := 1 + 2 should produce a KindExprBinary for "1 + 2"
	binaries := g.NodesByKind(graph.KindExprBinary)
	if len(binaries) == 0 {
		t.Fatal("expected at least one KindExprBinary node")
	}

	// Find the "1 + 2" binary node.
	var addNode *graph.Node
	for _, n := range binaries {
		if n.Metadata["src"] == "1 + 2" {
			addNode = n
		}
	}
	if addNode == nil {
		t.Error("no KindExprBinary with src '1 + 2' found")
	} else {
		if addNode.Metadata["op"] != "+" {
			t.Errorf("expected op '+', got %q", addNode.Metadata["op"])
		}
		if addNode.Metadata["lhs_src"] != "1" {
			t.Errorf("expected lhs_src '1', got %q", addNode.Metadata["lhs_src"])
		}
		if addNode.Metadata["rhs_src"] != "2" {
			t.Errorf("expected rhs_src '2', got %q", addNode.Metadata["rhs_src"])
		}
	}

	// --- Binary inside if cond (x > 0) ---

	var condNode *graph.Node
	for _, n := range binaries {
		if n.Metadata["src"] == "x > 0" {
			condNode = n
		}
	}
	if condNode == nil {
		t.Error("no KindExprBinary with src 'x > 0' found")
	} else {
		if condNode.Metadata["op"] != ">" {
			t.Errorf("expected op '>', got %q", condNode.Metadata["op"])
		}
	}

	// --- EdgeContains from KindIf to the binary cond expression ---
	ifNodes := g.NodesByKind(graph.KindIf)
	if len(ifNodes) == 0 {
		t.Fatal("no KindIf nodes found")
	}
	var ifHasBinaryChild bool
	for _, ifn := range ifNodes {
		for _, e := range g.EdgesFrom(ifn.ID) {
			if e.Kind != graph.EdgeContains {
				continue
			}
			child, ok := g.GetNode(e.To)
			if !ok {
				continue
			}
			if child.Kind == graph.KindExprBinary && child.Metadata["src"] == "x > 0" {
				ifHasBinaryChild = true
			}
		}
	}
	if !ifHasBinaryChild {
		t.Error("KindIf does not have KindExprBinary child for condition 'x > 0'")
	}

	// --- Recursive children: KindExprIdent "x" under "x > 0" binary ---
	if condNode != nil {
		children := g.Children(condNode.ID)
		var hasIdent bool
		for _, c := range children {
			if c.Kind == graph.KindExprIdent && c.Metadata["name"] == "x" {
				hasIdent = true
			}
		}
		if !hasIdent {
			t.Error("KindExprBinary 'x > 0' does not have KindExprIdent child for 'x'")
		}
	}

	// --- KindExprLiteral for "0" under "x > 0" ---
	if condNode != nil {
		children := g.Children(condNode.ID)
		var hasLit bool
		for _, c := range children {
			if c.Kind == graph.KindExprLiteral && c.Metadata["src"] == "0" {
				hasLit = true
			}
		}
		if !hasLit {
			t.Error("KindExprBinary 'x > 0' does not have KindExprLiteral child for '0'")
		}
	}

	// --- KindAssign: binary child linked from assign node ---
	assignNodes := g.NodesByKind(graph.KindAssign)
	var assignHasBinaryChild bool
	for _, an := range assignNodes {
		for _, e := range g.EdgesFrom(an.ID) {
			if e.Kind != graph.EdgeContains {
				continue
			}
			child, ok := g.GetNode(e.To)
			if !ok {
				continue
			}
			if child.Kind == graph.KindExprBinary && child.Metadata["src"] == "1 + 2" {
				assignHasBinaryChild = true
			}
		}
	}
	if !assignHasBinaryChild {
		t.Error("KindAssign does not have KindExprBinary child for '1 + 2'")
	}

	// --- KindExprIndex for items[0] in return ---
	if count(graph.KindExprIndex) == 0 {
		t.Error("expected at least one KindExprIndex node for items[0]")
	}

	// --- KindReturn has KindExprIndex child ---
	returnNodes := g.NodesByKind(graph.KindReturn)
	var returnHasIndex bool
	for _, rn := range returnNodes {
		for _, e := range g.EdgesFrom(rn.ID) {
			if e.Kind != graph.EdgeContains {
				continue
			}
			child, ok := g.GetNode(e.To)
			if !ok {
				continue
			}
			if child.Kind == graph.KindExprIndex {
				returnHasIndex = true
			}
		}
	}
	if !returnHasIndex {
		t.Error("KindReturn does not have KindExprIndex child for items[0]")
	}

	// --- SendStmt: KindSend has KindExprBinary child for "v + 1" ---
	sendNodes := g.NodesByKind(graph.KindSend)
	var sendHasBinary bool
	for _, sn := range sendNodes {
		for _, e := range g.EdgesFrom(sn.ID) {
			if e.Kind != graph.EdgeContains {
				continue
			}
			child, ok := g.GetNode(e.To)
			if !ok {
				continue
			}
			if child.Kind == graph.KindExprBinary && child.Metadata["src"] == "v + 1" {
				sendHasBinary = true
			}
		}
	}
	if !sendHasBinary {
		t.Error("KindSend does not have KindExprBinary child for 'v + 1'")
	}

	// --- ForStmt cond: KindFor has KindExprBinary child for "i < len(items)" ---
	// Note: "i < len(items)" contains a call — addExpr skips *ast.CallExpr but
	// still creates the binary wrapping it if the call is not the top-level expr.
	// The binary node "i < len(items)" is created; the call sub-expr is skipped.
	forNodes := g.NodesByKind(graph.KindFor)
	var forHasBinaryChild bool
	for _, fn := range forNodes {
		for _, e := range g.EdgesFrom(fn.ID) {
			if e.Kind != graph.EdgeContains {
				continue
			}
			child, ok := g.GetNode(e.To)
			if !ok {
				continue
			}
			if child.Kind == graph.KindExprBinary && child.Metadata["op"] == "<" {
				forHasBinaryChild = true
			}
		}
	}
	if !forHasBinaryChild {
		t.Error("KindFor does not have KindExprBinary child for 'i < len(items)'")
	}

	// --- ID format: expr:bin:<fileID>:<line>:<col> ---
	for _, n := range binaries {
		if len(n.ID) < 9 || n.ID[:9] != "expr:bin:" {
			t.Errorf("KindExprBinary has unexpected ID prefix: %q", n.ID)
		}
	}

	t.Logf("binary=%d ident=%d literal=%d index=%d",
		count(graph.KindExprBinary),
		count(graph.KindExprIdent),
		count(graph.KindExprLiteral),
		count(graph.KindExprIndex),
	)
}
