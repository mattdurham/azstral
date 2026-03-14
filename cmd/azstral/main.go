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
	root := flag.String("root", defaultRoot(), "working directory to auto-parse on startup")
	flag.Parse()

	srv, err := server.New(*dbPath, *root)
	if err != nil {
		log.Fatal(err)
	}

	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

func defaultRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
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
