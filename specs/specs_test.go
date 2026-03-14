package specs

import "testing"

func TestExtract(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"SPEC-004: Print hello world", 1},
		{"NOTE-001 and SPEC-002", 2},
		{"no specs here", 0},
		{"BENCH-001 is fast", 1},
		{"TEST-006: Verify output", 1},
	}
	for _, tt := range tests {
		got := Extract(tt.input)
		if len(got) != tt.want {
			t.Errorf("Extract(%q) = %d idents, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestIsSpecComment(t *testing.T) {
	if !IsSpecComment("// SPEC-001: something") {
		t.Error("expected true for SPEC comment")
	}
	if IsSpecComment("// just a comment") {
		t.Error("expected false for plain comment")
	}
}

func TestNormalizeComment(t *testing.T) {
	if got := NormalizeComment("// hello"); got != "hello" {
		t.Errorf("got %q", got)
	}
	if got := NormalizeComment("/* block */"); got != "block" {
		t.Errorf("got %q", got)
	}
}
