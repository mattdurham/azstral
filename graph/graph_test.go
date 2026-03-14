package graph

import "testing"

func TestAddAndGetNode(t *testing.T) {
	g := New()
	n := &Node{ID: "func:main", Kind: KindFunction, Name: "main"}
	if err := g.AddNode(n); err != nil {
		t.Fatal(err)
	}
	got, ok := g.GetNode("func:main")
	if !ok {
		t.Fatal("node not found")
	}
	if got.Name != "main" {
		t.Errorf("got name %q, want %q", got.Name, "main")
	}
}

func TestDuplicateNode(t *testing.T) {
	g := New()
	n := &Node{ID: "x", Kind: KindFile, Name: "x"}
	g.AddNode(n)
	if err := g.AddNode(n); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestAddEdge(t *testing.T) {
	g := New()
	g.AddNode(&Node{ID: "a", Kind: KindFile, Name: "a"})
	g.AddNode(&Node{ID: "b", Kind: KindFunction, Name: "b"})
	if err := g.AddEdge("a", "b", EdgeContains); err != nil {
		t.Fatal(err)
	}
	edges := g.EdgesFrom("a")
	if len(edges) != 1 || edges[0].To != "b" {
		t.Fatal("edge not found")
	}
}

func TestNodesByKind(t *testing.T) {
	g := New()
	g.AddNode(&Node{ID: "f1", Kind: KindFunction, Name: "f1"})
	g.AddNode(&Node{ID: "f2", Kind: KindFunction, Name: "f2"})
	g.AddNode(&Node{ID: "p1", Kind: KindPackage, Name: "p1"})

	fns := g.NodesByKind(KindFunction)
	if len(fns) != 2 {
		t.Errorf("got %d functions, want 2", len(fns))
	}
}
