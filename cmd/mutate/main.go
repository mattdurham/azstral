// Package main mutates cmd/hello/main.go via the azstral MCP server.
// It does not touch the source file directly — all changes go through graph nodes.
package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"runtime"

	"github.com/matt/azstral/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	root := projectRoot()
	helloPath := filepath.Join(root, "cmd", "hello", "main.go")

	srv, err := server.New(":memory:")
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	clientT, serverT := mcp.NewInMemoryTransports()
	go srv.Connect(ctx, serverT, nil)

	client := mcp.NewClient(&mcp.Implementation{Name: "mutate-cli"}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	call := func(tool string, args map[string]any) string {
		r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			log.Fatalf("%s: %v", tool, err)
		}
		if r.IsError {
			log.Fatalf("%s error: %v", tool, r.Content)
		}
		return r.Content[0].(*mcp.TextContent).Text
	}

	// 1. Parse the file into the graph.
	fmt.Println("→ parse_file", helloPath)
	fmt.Println(call("parse_file", map[string]any{"path": helloPath}))

	// 2. Show current function node.
	before := call("get_node", map[string]any{"id": "func:main"})
	fmt.Println("→ func:main BEFORE:\n", before)

	// 3. Mutate: update the function body text.
	fmt.Println("→ update_node func:main text")
	call("update_node", map[string]any{
		"id":   "func:main",
		"text": `fmt.Println("Hello World!")`,
	})

	// 4. Show updated node.
	after := call("get_node", map[string]any{"id": "func:main"})
	fmt.Println("→ func:main AFTER:\n", after)

	// 5. Render and write back.
	fmt.Println("→ write_file", helloPath)
	fmt.Println(call("write_file", map[string]any{
		"file_node_id": "file:main.go",
		"output_path":  helloPath,
	}))
}

func projectRoot() string {
	_, file, _, _ := runtime.Caller(0)
	// file = .../azstral/cmd/mutate/main.go
	return filepath.Join(filepath.Dir(file), "..", "..")
}
