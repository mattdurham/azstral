// Package main runs the azstral MCP server over stdio.
// SPEC-003: Expose the code graph via an MCP server.
// NOTE-003: The MCP server communicates via stdin/stdout JSON-RPC.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/matt/azstral/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	dbPath := flag.String("db", defaultDBPath(), "path to SQLite database")
	flag.Parse()

	srv, err := server.New(*dbPath)
	if err != nil {
		log.Fatal(err)
	}

	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "azstral.db"
	}
	dir := filepath.Join(home, ".azstral")
	os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "azstral.db")
}
