// Package main rebuilds gobyexample programs through the azstral graph.
// No source text is written directly — all code flows through parse→graph→render.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/matt/azstral/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	sourceRoot = "/home/matt/source/gobyexample/examples"
	destRoot   = "/home/matt/source/azstral/cmd/redogobyexample"
)

func main() {
	for _, ex := range []struct{ name, slug string }{
		{"Hello World", "hello-world"},
		{"Values", "values"},
		{"Variables", "variables"},
		{"Constants", "constants"},
		{"For", "for"},
		{"If/Else", "if-else"},
		{"Switch", "switch"},
		{"Arrays", "arrays"},
		{"Slices", "slices"},
		{"Maps", "maps"},
		{"Functions", "functions"},
		{"Multiple Return Values", "multiple-return-values"},
		{"Variadic Functions", "variadic-functions"},
		{"Closures", "closures"},
		{"Recursion", "recursion"},
		{"Range over Built-in Types", "range-over-built-in-types"},
		{"Pointers", "pointers"},
		{"Strings and Runes", "strings-and-runes"},
		{"Structs", "structs"},
		{"Methods", "methods"},
		{"Interfaces", "interfaces"},
		{"Enums", "enums"},
		{"Struct Embedding", "struct-embedding"},
		{"Generics", "generics"},
		{"Range over Iterators", "range-over-iterators"},
		{"Errors", "errors"},
		{"Custom Errors", "custom-errors"},
		{"Goroutines", "goroutines"},
		{"Channels", "channels"},
		{"Channel Buffering", "channel-buffering"},
		{"Channel Synchronization", "channel-synchronization"},
		{"Channel Directions", "channel-directions"},
		{"Select", "select"},
		{"Timeouts", "timeouts"},
		{"Non-Blocking Channel Operations", "non-blocking-channel-operations"},
		{"Closing Channels", "closing-channels"},
		{"Range over Channels", "range-over-channels"},
		{"Timers", "timers"},
		{"Tickers", "tickers"},
		{"Worker Pools", "worker-pools"},
		{"WaitGroups", "waitgroups"},
		{"Rate Limiting", "rate-limiting"},
		{"Atomic Counters", "atomic-counters"},
		{"Mutexes", "mutexes"},
		{"Stateful Goroutines", "stateful-goroutines"},
		{"Sorting", "sorting"},
		{"Sorting by Functions", "sorting-by-functions"},
		{"Panic", "panic"},
		{"Defer", "defer"},
		{"Recover", "recover"},
		{"String Functions", "string-functions"},
		{"String Formatting", "string-formatting"},
		{"Text Templates", "text-templates"},
		{"Regular Expressions", "regular-expressions"},
		{"JSON", "json"},
		{"XML", "xml"},
		{"Time", "time"},
		{"Epoch", "epoch"},
		{"Time Formatting / Parsing", "time-formatting-parsing"},
		{"Random Numbers", "random-numbers"},
		{"Number Parsing", "number-parsing"},
		{"URL Parsing", "url-parsing"},
		{"SHA256 Hashes", "sha256-hashes"},
		{"Base64 Encoding", "base64-encoding"},
		{"Reading Files", "reading-files"},
		{"Writing Files", "writing-files"},
		{"Line Filters", "line-filters"},
		{"File Paths", "file-paths"},
		{"Directories", "directories"},
		{"Temporary Files and Directories", "temporary-files-and-directories"},
		{"Embed Directive", "embed-directive"},
		{"Testing and Benchmarking", "testing-and-benchmarking"},
		{"Command-Line Arguments", "command-line-arguments"},
		{"Command-Line Flags", "command-line-flags"},
		{"Command-Line Subcommands", "command-line-subcommands"},
		{"Environment Variables", "environment-variables"},
		{"Logging", "logging"},
		{"HTTP Client", "http-client"},
		{"HTTP Server", "http-server"},
		{"TCP Server", "tcp-server"},
		{"Context", "context"},
		{"Spawning Processes", "spawning-processes"},
		{"Exec'ing Processes", "execing-processes"},
		{"Signals", "signals"},
		{"Exit", "exit"},
	} {
		if err := processExample(ex.name, ex.slug); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] error: %v\n", ex.name, err)
			os.Exit(1)
		}
	}
}

func processExample(name, slug string) error {
	// Most examples use slug+".go"; testing-and-benchmarking uses main_test.go.
	srcBase := slug + ".go"
	dstBase := "main.go"
	if slug == "testing-and-benchmarking" {
		srcBase = "main_test.go"
		dstBase = "main_test.go"
	}
	srcFile := filepath.Join(sourceRoot, slug, srcBase)
	dstFile := filepath.Join(destRoot, slug, dstBase)

	// Fresh server + in-memory session per example.
	srv, err := server.New(":memory:", "")
	if err != nil {
		return err
	}
	ctx := context.Background()
	clientT, serverT := mcp.NewInMemoryTransports()
	go srv.Connect(ctx, serverT, nil)

	client := mcp.NewClient(&mcp.Implementation{Name: "redogobyexample"}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		return err
	}
	defer session.Close()

	call := func(tool string, args map[string]any) (string, error) {
		r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			return "", fmt.Errorf("tool %s: %w", tool, err)
		}
		text := r.Content[0].(*mcp.TextContent).Text
		if r.IsError {
			return "", fmt.Errorf("tool %s: %s", tool, text)
		}
		return text, nil
	}

	// Parse the gobyexample source into the graph.
	result, err := call("parse_file", map[string]any{"path": srcFile})
	if err != nil {
		return err
	}
	fmt.Printf("[%s] %s\n", name, result)

	// The file node ID is based on the source filename.
	fileNodeID := "file:" + srcBase

	// Render and write.
	result, err = call("write_file", map[string]any{
		"file_node_id": fileNodeID,
		"output_path":  dstFile,
	})
	if err != nil {
		return err
	}
	fmt.Printf("[%s] %s\n", name, result)

	// Compare with original.
	orig, err := os.ReadFile(srcFile)
	if err != nil {
		return err
	}
	generated, err := os.ReadFile(dstFile)
	if err != nil {
		return err
	}

	if string(orig) == string(generated) {
		fmt.Printf("[%s] ✓ exact match\n", name)
	} else {
		fmt.Printf("[%s] ✗ mismatch\n", name)
		printDiff(string(orig), string(generated))
	}
	return nil
}

func printDiff(orig, gen string) {
	origLines := strings.Split(orig, "\n")
	genLines := strings.Split(gen, "\n")
	maxLen := len(origLines)
	if len(genLines) > maxLen {
		maxLen = len(genLines)
	}
	fmt.Println("  --- original")
	fmt.Println("  +++ generated")
	for i := range maxLen {
		o, g := "", ""
		if i < len(origLines) {
			o = origLines[i]
		}
		if i < len(genLines) {
			g = genLines[i]
		}
		if o != g {
			fmt.Printf("  %3d - %q\n", i+1, o)
			fmt.Printf("  %3d + %q\n", i+1, g)
		}
	}
}
