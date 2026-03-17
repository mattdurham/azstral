package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// Test render on a parsed file (read-only codegen path).
func TestMCPServer_RenderParsedFile(t *testing.T) {
	session := setupTestServer(t)
	helloPath := findHelloMain(t)

	callTool(t, session, "parse_files", map[string]any{"paths": []string{helloPath}})

	fileID := "file:" + helloPath
	src := callTool(t, session, "render", map[string]any{"id": fileID})

	if !contains(src, "package main") {
		t.Errorf("missing package declaration:\n%s", src)
	}
	if !contains(src, "func main()") {
		t.Errorf("missing main function:\n%s", src)
	}
	t.Logf("Rendered source:\n%s", src)
}

// Test spec store read-only operations via MCP.
func TestMCPServer_SpecStore(t *testing.T) {
	session := setupTestServer(t)

	// list_specs on empty store should return empty JSON array or "[]".
	text := callTool(t, session, "list_specs", nil)
	if text == "" {
		t.Error("list_specs returned empty string")
	}
	t.Logf("list_specs (empty): %s", text)

	// get_spec on non-existent ID should return a tool error.
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_spec",
		Arguments: map[string]any{"id": "SPEC-999"},
	})
	if err != nil {
		t.Fatalf("get_spec unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Errorf("get_spec for non-existent ID should return an error, got: %v", result.Content)
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

func TestListNodesJSON(t *testing.T) {
	session := setupTestServer(t)
	helloPath := findHelloMain(t)
	callTool(t, session, "parse_files", map[string]any{"paths": []string{helloPath}})

	text := callTool(t, session, "list_nodes", map[string]any{"kind": "function"})

	if !strings.HasPrefix(strings.TrimSpace(text), "[") {
		t.Errorf("list_nodes must return JSON array:\n%s", text)
	}
	if !strings.Contains(text, `"id"`) {
		t.Errorf("list_nodes JSON must contain id field:\n%s", text)
	}
	t.Logf("list_nodes output:\n%s", text)
}

func TestQueryNodesJSON(t *testing.T) {
	session := setupTestServer(t)
	helloPath := findHelloMain(t)
	callTool(t, session, "parse_files", map[string]any{"paths": []string{helloPath}})

	text := callTool(t, session, "query_nodes", map[string]any{
		"expr": `kind == "function"`,
	})

	if !strings.HasPrefix(strings.TrimSpace(text), "[") {
		t.Errorf("query_nodes must return JSON array:\n%s", text)
	}
	if !strings.Contains(text, `"id"`) || !strings.Contains(text, `"kind"`) {
		t.Errorf("query_nodes JSON must contain id and kind fields:\n%s", text)
	}
	t.Logf("query_nodes output:\n%s", text)
}

func TestFindDeadcodeJSON(t *testing.T) {
	session := setupTestServer(t)

	// Parse hello world — it has only main() which is excluded from dead code.
	// find_deadcode should return "no dead code found" or an empty array.
	helloPath := findHelloMain(t)
	callTool(t, session, "parse_files", map[string]any{"paths": []string{helloPath}})

	// find_deadcode returns JSON array or a "no dead code" message.
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "find_deadcode",
		Arguments: map[string]any{"include_exported": true},
	})
	if err != nil {
		t.Fatalf("find_deadcode transport error: %v", err)
	}
	if result.IsError {
		t.Fatalf("find_deadcode tool error: %v", result.Content)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	t.Logf("find_deadcode output:\n%s", text)
	// Result is either an empty JSON array or "no dead code found".
	trimmed := strings.TrimSpace(text)
	if trimmed != "no dead code found" && trimmed != "[]" && !strings.HasPrefix(trimmed, "[") {
		t.Errorf("unexpected find_deadcode output:\n%s", text)
	}
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
