package store

import "testing"

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestCreateAndGetSpec(t *testing.T) {
	st := openTestStore(t)

	sp := &Spec{ID: "SPEC-001", Kind: "SPEC", Namespace: "", Title: "Parse Go", Body: "details"}
	if err := st.CreateSpec(sp); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetSpec("SPEC-001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Parse Go" {
		t.Errorf("title = %q", got.Title)
	}
}

func TestDuplicateSpec(t *testing.T) {
	st := openTestStore(t)
	sp := &Spec{ID: "SPEC-001", Kind: "SPEC", Title: "A"}
	st.CreateSpec(sp)
	if err := st.CreateSpec(sp); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestListSpecsByNamespace(t *testing.T) {
	st := openTestStore(t)
	st.CreateSpec(&Spec{ID: "SPEC-001", Kind: "SPEC", Namespace: "", Title: "root"})
	st.CreateSpec(&Spec{ID: "SPEC-020", Kind: "SPEC", Namespace: "io", Title: "io spec"})
	st.CreateSpec(&Spec{ID: "NOTE-001", Kind: "NOTE", Namespace: "io", Title: "io note"})

	// All.
	all, _ := st.ListSpecs("", "")
	if len(all) != 3 {
		t.Errorf("got %d specs, want 3", len(all))
	}

	// By namespace.
	io, _ := st.ListSpecs("", "io")
	if len(io) != 2 {
		t.Errorf("got %d io specs, want 2", len(io))
	}

	// By kind.
	notes, _ := st.ListSpecs("NOTE", "")
	if len(notes) != 1 {
		t.Errorf("got %d notes, want 1", len(notes))
	}
}

func TestLinkSpec(t *testing.T) {
	st := openTestStore(t)
	st.CreateSpec(&Spec{ID: "SPEC-004", Kind: "SPEC", Title: "Hello"})

	if err := st.LinkSpec("SPEC-004", "func:main"); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkSpec("SPEC-004", "file:main.go"); err != nil {
		t.Fatal(err)
	}

	links, _ := st.GetLinks("SPEC-004")
	if len(links) != 2 {
		t.Errorf("got %d links, want 2", len(links))
	}

	specs, _ := st.GetSpecsForNode("func:main")
	if len(specs) != 1 || specs[0].ID != "SPEC-004" {
		t.Errorf("GetSpecsForNode: got %v", specs)
	}
}

func TestDeleteSpec(t *testing.T) {
	st := openTestStore(t)
	st.CreateSpec(&Spec{ID: "SPEC-099", Kind: "SPEC", Title: "temp"})
	st.LinkSpec("SPEC-099", "func:foo")

	if err := st.DeleteSpec("SPEC-099"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetSpec("SPEC-099"); err == nil {
		t.Fatal("expected not found after delete")
	}
	links, _ := st.GetLinks("SPEC-099")
	if len(links) != 0 {
		t.Fatal("links not cleaned up")
	}
}
