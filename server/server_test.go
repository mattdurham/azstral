package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/matt/azstral/graph"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func setupTestServer(t *testing.T) *mcp.ClientSession {
	t.Helper()
	srv, err := New(":memory:", "")
	if err != nil {
		t.Fatal(err)
	}

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	go srv.Connect(ctx, serverTransport, nil)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("tool %s error: %v", name, result.Content)
	}
	return result.Content[0].(*mcp.TextContent).Text
}

// TEST-001, TEST-003, TEST-004, TEST-005: Parse and query via MCP.
func TestMCPServer_ParseAndQuery(t *testing.T) {
	session := setupTestServer(t)
	helloPath := findHelloMain(t)

	callTool(t, session, "parse_files", map[string]any{"paths": []string{helloPath}})

	// Full graph.
	text := callTool(t, session, "get_graph", nil)
	var g graph.Graph
	if err := json.Unmarshal([]byte(text), &g); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(g.Nodes) == 0 {
		t.Fatal("graph has no nodes")
	}

	// Specific nodes.
	callTool(t, session, "get_nodes", map[string]any{"ids": []string{"func:main"}})

	// List by kind.
	callTool(t, session, "list_nodes", map[string]any{"kind": "function"})
}

// Test building hello world entirely through graph mutation + render.
func TestMCPServer_BuildHelloWorld(t *testing.T) {
	session := setupTestServer(t)

	// 1. Create specs in the store.
	callTool(t, session, "create_spec", map[string]any{
		"id": "SPEC-004", "kind": "SPEC", "namespace": "",
		"title": "Print Hello World to the console",
	})
	callTool(t, session, "create_spec", map[string]any{
		"id": "NOTE-004", "kind": "NOTE", "namespace": "",
		"title": "Hello world is a test fixture and proof of concept",
	})

	// 2. Build graph nodes.
	callTool(t, session, "add_nodes", map[string]any{"nodes": []map[string]any{
		{"id": "pkg:main", "kind": "package", "name": "main"},
		{"id": "file:main.go", "kind": "file", "name": "main.go", "line": 1},
		{"id": "import:fmt", "kind": "import", "name": "fmt", "line": 10},
		{"id": "func:main", "kind": "function", "name": "main",
			"text": "fmt.Println(\"Hello World\")", "line": 20,
			"metadata": map[string]any{"params": "()", "returns": ""}},
	}})
	callTool(t, session, "add_edges", map[string]any{"edges": []map[string]any{
		{"from": "pkg:main", "to": "file:main.go", "kind": "contains"},
		{"from": "file:main.go", "to": "import:fmt", "kind": "contains"},
		{"from": "file:main.go", "to": "func:main", "kind": "contains"},
	}})

	// 3. Link specs to code.
	callTool(t, session, "link_spec", map[string]any{
		"spec_id": "SPEC-004", "node_id": "func:main",
	})
	callTool(t, session, "link_spec", map[string]any{
		"spec_id": "NOTE-004", "node_id": "file:main.go",
	})

	// 4. Render.
	src := callTool(t, session, "render", map[string]any{"id": "file:main.go"})

	// Verify the output.
	if !contains(src, "package main") {
		t.Errorf("missing package declaration:\n%s", src)
	}
	if !contains(src, `"fmt"`) {
		t.Errorf("missing fmt import:\n%s", src)
	}
	if !contains(src, "func main()") {
		t.Errorf("missing main function:\n%s", src)
	}
	if !contains(src, `fmt.Println("Hello World")`) {
		t.Errorf("missing Println statement:\n%s", src)
	}
	if !contains(src, "SPEC-004") {
		t.Errorf("missing SPEC-004 comment:\n%s", src)
	}

	t.Logf("Generated source:\n%s", src)
}

// Test mutating hello world through the graph — no direct file edits.
func TestMCPServer_MutateHelloWorld(t *testing.T) {
	session := setupTestServer(t)

	// 1. Build the hello world graph (same as BuildHelloWorld).
	callTool(t, session, "create_spec", map[string]any{
		"id": "SPEC-004", "kind": "SPEC", "namespace": "",
		"title": "Print Hello World! to the console",
	})
	callTool(t, session, "add_nodes", map[string]any{"nodes": []map[string]any{
		{"id": "pkg:main", "kind": "package", "name": "main"},
		{"id": "file:main.go", "kind": "file", "name": "main.go", "line": 1},
		{"id": "import:fmt", "kind": "import", "name": "fmt", "line": 10},
		{"id": "func:main", "kind": "function", "name": "main",
			"text": `fmt.Println("Hello World")`, "line": 20,
			"metadata": map[string]any{"params": "()", "returns": ""}},
	}})
	callTool(t, session, "add_edges", map[string]any{"edges": []map[string]any{
		{"from": "pkg:main", "to": "file:main.go", "kind": "contains"},
		{"from": "file:main.go", "to": "import:fmt", "kind": "contains"},
		{"from": "file:main.go", "to": "func:main", "kind": "contains"},
	}})
	callTool(t, session, "link_spec", map[string]any{
		"spec_id": "SPEC-004", "node_id": "func:main",
	})

	// Verify original.
	src := callTool(t, session, "render", map[string]any{"id": "file:main.go"})
	if !contains(src, `fmt.Println("Hello World")`) {
		t.Fatalf("original missing Hello World:\n%s", src)
	}
	t.Logf("BEFORE mutation:\n%s", src)

	// 2. Mutate: update the function body text via update_nodes.
	callTool(t, session, "update_nodes", map[string]any{"nodes": []map[string]any{
		{"id": "func:main", "text": `fmt.Println("Hello World!")`},
	}})

	// 3. Render again — should now have the exclamation mark.
	src = callTool(t, session, "render", map[string]any{"id": "file:main.go"})
	t.Logf("AFTER mutation:\n%s", src)

	if !contains(src, `fmt.Println("Hello World!")`) {
		t.Errorf("mutation failed — missing exclamation:\n%s", src)
	}
	if contains(src, `"Hello World")`) && !contains(src, `"Hello World!")`) {
		t.Errorf("old text still present:\n%s", src)
	}
}

// Test spec store CRUD via MCP.
func TestMCPServer_SpecStore(t *testing.T) {
	session := setupTestServer(t)

	// Create at root namespace.
	callTool(t, session, "create_spec", map[string]any{
		"id": "SPEC-001", "kind": "SPEC", "namespace": "",
		"title": "Parse Go into graph", "body": "The system shall parse Go source.",
	})

	// Create in io namespace.
	callTool(t, session, "create_spec", map[string]any{
		"id": "SPEC-020", "kind": "SPEC", "namespace": "io",
		"title": "IO module reads files",
	})

	// List all.
	text := callTool(t, session, "list_specs", nil)
	if !contains(text, "SPEC-001") || !contains(text, "SPEC-020") {
		t.Errorf("list_specs missing specs:\n%s", text)
	}

	// List by namespace.
	text = callTool(t, session, "list_specs", map[string]any{"namespace": "io"})
	if !contains(text, "SPEC-020") {
		t.Errorf("list_specs namespace filter failed:\n%s", text)
	}
	if contains(text, "SPEC-001") {
		t.Errorf("list_specs namespace filter leaked root spec:\n%s", text)
	}

	// Get specific.
	text = callTool(t, session, "get_spec", map[string]any{"id": "SPEC-001"})
	if !contains(text, "Parse Go into graph") {
		t.Errorf("get_spec wrong content:\n%s", text)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && jsonOrText(s, substr)
}

func jsonOrText(s, substr string) bool {
	// Check both raw and JSON-escaped.
	if idx := indexOf(s, substr); idx >= 0 {
		return true
	}
	return false
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func findHelloMain(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for range 5 {
		candidate := filepath.Join(dir, "cmd", "hello", "main.go")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find cmd/hello/main.go")
	return ""
}
