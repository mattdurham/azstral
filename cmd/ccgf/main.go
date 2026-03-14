// Command ccgf parses Go source trees and outputs Compact Code Graph Format.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/matt/azstral/ccgf"
	"github.com/matt/azstral/graph"
	"github.com/matt/azstral/parser"
)

func main() {
	vendor := flag.String("vendor", "surface", "vendor mode: surface or include")
	scope := flag.String("scope", "program", "scope: program, file:<id>, type:<id>")
	attrs := flag.Bool("attrs", false, "include attribute lines")
	module := flag.String("mod", "", "module path for header")
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintf(os.Stderr, "usage: ccgf [flags] <dir|file> ...\n")
		os.Exit(1)
	}

	g := graph.New()

	start := time.Now()
	var fileCount int
	for _, arg := range flag.Args() {
		info, err := os.Stat(arg)
		if err != nil {
			log.Fatalf("stat %s: %v", arg, err)
		}
		if info.IsDir() {
			n, err := parseTree(g, arg)
			if err != nil {
				log.Fatalf("parse dir %s: %v", arg, err)
			}
			fileCount += n
		} else {
			if err := parser.ParseFile(g, arg); err != nil {
				log.Fatalf("parse file %s: %v", arg, err)
			}
			fileCount++
		}
	}
	parseTime := time.Since(start)

	fmt.Fprintf(os.Stderr, "parsed %d files → %d nodes, %d edges in %v\n",
		fileCount, len(g.Nodes), len(g.Edges), parseTime)

	opts := ccgf.Options{
		Scope:  *scope,
		Attrs:  *attrs,
		Module: *module,
	}
	if *vendor == "include" {
		opts.Vendor = ccgf.VendorInclude
	}

	start = time.Now()
	out := ccgf.Encode(g, opts)
	encodeTime := time.Since(start)

	fmt.Fprint(os.Stdout, out)
	fmt.Fprintf(os.Stderr, "ccgf: %d bytes in %v\n", len(out), encodeTime)
}

// parseTree recursively parses all .go files in a directory tree,
// skipping vendor, .git, and testdata directories.
func parseTree(g *graph.Graph, root string) (int, error) {
	var count int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", ".git", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		if perr := parser.ParseFile(g, path); perr != nil {
			fmt.Fprintf(os.Stderr, "warn: %s: %v\n", path, perr)
			return nil // continue on error
		}
		count++
		return nil
	})
	return count, err
}
