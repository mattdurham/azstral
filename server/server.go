// Package server provides the MCP server for azstral.
// SPEC-003: Expose the code graph via an MCP server.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/matt/azstral/ccgf"
	"github.com/matt/azstral/codegen"
	"github.com/matt/azstral/graph"
	"github.com/matt/azstral/parser"
	"github.com/matt/azstral/query"
	"github.com/matt/azstral/bench"
	"github.com/matt/azstral/escape"
	"github.com/matt/azstral/lint"
	"github.com/matt/azstral/races"
	"github.com/matt/azstral/store"
	"github.com/matt/azstral/testcov"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)
// New creates an MCP server with all azstral tools registered.
// root is the working directory for this server — it is auto-parsed on startup
// and used as the default path for parse_tree and reset_graph. Pass "" to skip
// auto-parse (useful for tests or when the caller will parse manually).
// The SQLite database is created at dbPath (use ":memory:" for in-memory).
func New(dbPath, root string) (*mcp.Server, error) {
	g := graph.New()
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	// Auto-parse the working root so the graph is ready immediately.
	var parseMsg string
	if root != "" {
		n, perr := parser.ParseTree(g, root)
		if perr != nil {
			parseMsg = fmt.Sprintf(" Warning: partial parse of %s (%d files loaded, error: %v).", root, n, perr)
		} else {
			parseMsg = fmt.Sprintf(" Parsed %d files from %s.", n, root)
		}
	}

	instructions := "Azstral represents Go code as a connected graph. " +
		"The graph is already loaded — use encode_ccgf or query_nodes to explore it. " +
		"Read-only: use parse_tree, query_nodes, encode_ccgf, run_tests, etc. to analyse code." +
		parseMsg
	if root != "" {
		instructions += fmt.Sprintf(" Working root: %s.", root)
	}

	srv := mcp.NewServer(
		&mcp.Implementation{
			Name:    "azstral",
			Version: "0.1.0",
		},
		&mcp.ServerOptions{Instructions: instructions},
	)

	registerParseTools(srv, g, root)
	registerQueryTools(srv, g, root)
	registerSpecTools(srv, g, st)
	registerCodegenTools(srv, g, st)
	registerCCGFTools(srv, g, root)
	registerCELTools(srv, g, root)
	registerGraphTools(srv, g, root)
	registerTestTools(srv, g, root)
	registerEscapeTools(srv, g, root)
	registerBenchTools(srv, g, root)
	registerHotspotTools(srv, g, root)
	registerRaceTools(srv, g)
	registerLintTools(srv, g, root)
	registerAnalysisTools(srv, g, root)
	registerCallGraphTools(srv, g)
	registerFindImplementations(srv, g)
	registerVetTools(srv, g, root)
	registerFindTodos(srv, g)
	registerSnapshotTools(srv, g)
	return srv, nil
}

// --- Input types ---
type pathInput struct {
	Path string `json:"path" jsonschema:"absolute file or directory path"`
}

type nodeIDInput struct {
	ID string `json:"id" jsonschema:"node ID (e.g. func:main, pkg:main, file:main.go)"`
}

type listNodesInput struct {
	Kind string `json:"kind,omitempty" jsonschema:"filter by node kind: package, file, function, type, variable, comment, spec, import, statement"`
}

type listEdgesInput struct {
	From string `json:"from,omitempty" jsonschema:"filter edges from this node ID"`
	To   string `json:"to,omitempty" jsonschema:"filter edges to this node ID"`
}

// --- Parse tools ---
func registerParseTools(srv *mcp.Server, g *graph.Graph, root string) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "parse_packages",
		Description: "Type-check the Go module and load all packages into the graph. " +
			"Slower than parse_tree (~seconds) but adds qualified_id metadata to every node, " +
			"enabling precise cross-package rename via rename_symbol. " +
			"Requires all module dependencies to be present (go mod download). " +
			"Resets the graph before loading.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		path := input.Path
		if path == "" {
			if root == "" {
				return toolError("path is required (no working root configured)"), nil, nil
			}
			path = root
		}
		g.Reset()
		n, err := parser.LoadPackages(g, path)
		if err != nil {
			// Partial results may still be useful — return as warning.
			return toolText(fmt.Sprintf("loaded %d files with errors: %v — %d nodes", n, err, len(g.Nodes))), nil, nil
		}
		return toolText(fmt.Sprintf("type-checked %d files — %d nodes (qualified_id available)", n, len(g.Nodes))), nil, nil
	})

	type parseFilesInput struct {
		Paths []string `json:"paths" jsonschema:"array of absolute file paths to parse"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "parse_files",
		Description: "Parse multiple Go source files in a single call.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input parseFilesInput) (*mcp.CallToolResult, any, error) {
		var errs []string
		parsed := 0
		for _, p := range input.Paths {
			if err := parser.ParseFile(g, p); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", p, err))
			} else {
				parsed++
			}
		}
		msg := fmt.Sprintf("parsed %d/%d files — %d nodes", parsed, len(input.Paths), len(g.Nodes))
		if len(errs) > 0 {
			msg += "; errors: " + strings.Join(errs, "; ")
		}
		return toolText(msg), nil, nil
	})

	desc := "Recursively parse all Go files under a directory tree. Skips vendor, .git, node_modules, and testdata directories."
	if root != "" {
		desc += fmt.Sprintf(" Omit path to re-parse the working root (%s).", root)
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "parse_tree",
		Description: desc,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		path := input.Path
		if path == "" {
			if root == "" {
				return toolError("path is required (no working root configured)"), nil, nil
			}
			path = root
		}
		// Reset before parsing so the graph contains exactly this tree —
		// not an accumulation of previously parsed projects.
		g.Reset()
		n, err := parser.ParseTree(g, path)
		if err != nil {
			return toolError(fmt.Sprintf("parse error: %v", err)), nil, nil
		}
		return toolText(fmt.Sprintf("parsed tree %s — %d files, %d nodes", path, n, len(g.Nodes))), nil, nil
	})
}

// --- Query tools ---
func registerQueryTools(srv *mcp.Server, g *graph.Graph, root string) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_graph",
		Description: "Return the full code graph as JSON.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		return toolJSON(g), nil, nil
	})

	type getNodesInput struct {
		IDs  []string `json:"ids,omitempty"  jsonschema:"node IDs to retrieve (strings like \"func:main.Parse\")"`
		Idxs []int    `json:"idxs,omitempty" jsonschema:"integer node indices to retrieve — shorter than full IDs (e.g. [1, 42, 87])"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_nodes",
		Description: "Return graph nodes by ID or integer index. " +
			"Use ids for known string IDs, idxs for the compact integers shown in query results. " +
			"Both can be combined in one call. Returns CCGF lines for each found node.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input getNodesInput) (*mcp.CallToolResult, any, error) {
		var b strings.Builder
		var missing []string

		writeNode := func(node *graph.Node) {
			r := toolNode(node, root, g)
			b.WriteString(r.Content[0].(*mcp.TextContent).Text)
			b.WriteString("\n\n")
		}

		for _, id := range input.IDs {
			node, ok := g.GetNode(id)
			if !ok {
				missing = append(missing, id)
				continue
			}
			writeNode(node)
		}
		for _, idx := range input.Idxs {
			node := g.NodeByIdx(idx)
			if node == nil {
				missing = append(missing, fmt.Sprintf("#%d", idx))
				continue
			}
			writeNode(node)
		}
		if len(missing) > 0 {
			fmt.Fprintf(&b, "not found: %s", strings.Join(missing, ", "))
		}
		return toolText(strings.TrimSpace(b.String())), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_nodes",
		Description: "List graph nodes, optionally filtered by kind.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input listNodesInput) (*mcp.CallToolResult, any, error) {
		var nodes []*graph.Node
		if input.Kind != "" {
			nodes = g.NodesByKind(graph.NodeKind(input.Kind))
		} else {
			for _, n := range g.Nodes {
				nodes = append(nodes, n)
			}
		}
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
		if len(nodes) == 0 {
			return toolText("no nodes"), nil, nil
		}
		return toolJSON(nodes), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_edges",
		Description: "List graph edges, optionally filtered by from/to node ID.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input listEdgesInput) (*mcp.CallToolResult, any, error) {
		if input.From != "" {
			return toolJSON(g.EdgesFrom(input.From)), nil, nil
		}
		if input.To != "" {
			return toolJSON(g.EdgesTo(input.To)), nil, nil
		}
		return toolJSON(g.Edges), nil, nil
	})
}


// --- Spec store tools ---
func registerSpecTools(srv *mcp.Server, g *graph.Graph, st *store.Store) {
	type getSpecInput struct {
		ID string `json:"id" jsonschema:"spec ID, e.g. SPEC-001"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_spec",
		Description: "Get a spec by its ID from the database.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input getSpecInput) (*mcp.CallToolResult, any, error) {
		sp, err := st.GetSpec(strings.ToUpper(input.ID))
		if err != nil {
			return toolError(err.Error()), nil, nil
		}
		return toolJSON(sp), nil, nil
	})

	type listSpecsInput struct {
		Kind      string `json:"kind,omitempty" jsonschema:"filter by kind: SPEC, NOTE, TEST, BENCH"`
		Namespace string `json:"namespace,omitempty" jsonschema:"filter by namespace"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_specs",
		Description: "List specs from the database, optionally filtered by kind and/or namespace.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input listSpecsInput) (*mcp.CallToolResult, any, error) {
		specs, err := st.ListSpecs(strings.ToUpper(input.Kind), input.Namespace)
		if err != nil {
			return toolError(err.Error()), nil, nil
		}
		return toolJSON(specs), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "find_spec",
		Description: "Find which graph nodes a spec covers, pulling data from both the store and the graph.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input getSpecInput) (*mcp.CallToolResult, any, error) {
		id := strings.ToUpper(input.ID)
		sp, err := st.GetSpec(id)
		if err != nil {
			return toolError(err.Error()), nil, nil
		}
		links, _ := st.GetLinks(id)

		type result struct {
			Spec  *store.Spec    `json:"spec"`
			Nodes []*graph.Node  `json:"nodes"`
			Links []string       `json:"links"`
		}
		r := result{Spec: sp, Links: links}
		for _, nodeID := range links {
			if n, ok := g.GetNode(nodeID); ok {
				r.Nodes = append(r.Nodes, n)
			}
		}
		return toolJSON(r), nil, nil
	})
}

// --- Codegen tools ---
func registerCodegenTools(srv *mcp.Server, g *graph.Graph, st *store.Store) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "render",
		Description: "Preview a file node as Go source. Read-only — use add_node/update_node to modify files.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input nodeIDInput) (*mcp.CallToolResult, any, error) {
		src, err := codegen.RenderFile(g, st, input.ID)
		if err != nil {
			return toolError(err.Error()), nil, nil
		}
		return toolText(src), nil, nil
	})
}

// --- Helpers ---

// toolNode renders a single graph node as JSON.
func toolNode(n *graph.Node, _ string, graphs ...*graph.Graph) *mcp.CallToolResult {
	if n == nil {
		return toolError("node is nil")
	}
	type nodeOut struct {
		*graph.Node
		Idx int `json:"idx,omitempty"`
	}
	out := nodeOut{Node: n}
	if len(graphs) > 0 && graphs[0] != nil {
		out.Idx = graphs[0].NodeIdx(n.ID)
	}
	return toolJSON(out)
}

func toolText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
		IsError: true,
	}
}

func toolJSON(v any) *mcp.CallToolResult {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return toolError(fmt.Sprintf("json marshal error: %v", err))
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
	}
}

// --- CCGF tools ---
func registerCCGFTools(srv *mcp.Server, g *graph.Graph, root string) {
	type ccgfInput struct {
		Scope  string `json:"scope,omitempty" jsonschema:"scope: 'program' (default), 'file:<id>' for single file, 'type:<id>' for single type and its methods"`
		Vendor string `json:"vendor,omitempty" jsonschema:"vendor mode: 'surface' (default, 1 layer of API used), 'include' (full vendor tree)"`
		Attrs  bool   `json:"attrs,omitempty" jsonschema:"emit attribute lines (sig, loc, kind, ro)"`
		Module string `json:"module,omitempty" jsonschema:"module path for header"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "encode_ccgf",
		Description: "Encode the graph in Compact Code Graph Format (CCGF). " +
			"Returns a compact, line-based representation of program structure " +
			"with typed nodes, typed edges, and optional attributes. " +
			"Much smaller than JSON for LLM consumption. " +
			"Call ccgf_grammar first if you need the format definition.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ccgfInput) (*mcp.CallToolResult, any, error) {
		opts := ccgf.Options{
			Scope:  input.Scope,
			Attrs:  input.Attrs,
			Module: input.Module,
			Root:   root,
		}
		switch strings.ToLower(input.Vendor) {
		case "include":
			opts.Vendor = ccgf.VendorInclude
		default:
			opts.Vendor = ccgf.VendorSurface
		}
		return toolText(ccgf.Encode(g, opts)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ccgf_grammar",
		Description: "Return the CCGF format grammar definition. Call this before encode_ccgf if you need to understand the output format.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		return toolText(ccgf.Grammar), nil, nil
	})

	type deadcodeInput struct {
		IncludeExported bool `json:"include_exported,omitempty" jsonschema:"report exported symbols in non-main packages as dead (default: false, since they may be used externally)"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "find_deadcode",
		Description: "Find dead code — symbols that are defined but never referenced. " +
			"Excludes main(), init(), Test*/Benchmark*/Example* functions. " +
			"By default, exported symbols in non-main packages are excluded " +
			"(they may be used by external consumers).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input deadcodeInput) (*mcp.CallToolResult, any, error) {
		dead := ccgf.FindDeadCode(g, input.IncludeExported)
		if len(dead) == 0 {
			return toolText("no dead code found"), nil, nil
		}
		return toolJSON(dead), nil, nil
	})
}

// --- CEL query tools ---

func registerCELTools(srv *mcp.Server, g *graph.Graph, root string) {
	type celInput struct {
		Expr   string `json:"expr" jsonschema:"CEL expression to evaluate against each node or edge. Call query_help for available fields and examples."`
		SortBy string `json:"sort_by,omitempty" jsonschema:"field to sort results by (e.g. cyclomatic, cognitive, coverage, heap_allocs, bench_ns_op, name, line). Numeric fields sort descending by default."`
		TopN   int    `json:"top_n,omitempty"   jsonschema:"return only the top N results after sorting (descending). Use with sort_by."`
		BottomN int   `json:"bottom_n,omitempty" jsonschema:"return only the bottom N results after sorting (ascending). Use with sort_by."`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "query_nodes",
		Description: "Query graph nodes using a CEL expression. " +
			"Returns all nodes where the expression is true. " +
			"Use sort_by with top_n/bottom_n to rank results: " +
			"e.g. sort_by=\"cyclomatic\" top_n=10 returns the 10 most complex functions. " +
			"Available fields: id, kind, name, file, line, text, external, " +
			"cyclomatic, cognitive, receiver, params, returns, parent_id, " +
			"callee_ids, caller_ids, child_ids. " +
			"Call query_help for full documentation and examples.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input celInput) (*mcp.CallToolResult, any, error) {
		if input.Expr == "" {
			return toolError("expr is required"), nil, nil
		}
		nodes, err := query.NodeQuery(g, input.Expr)
		if err != nil {
			return toolError(fmt.Sprintf("query error: %v", err)), nil, nil
		}
		if len(nodes) == 0 {
			return toolText("no matches"), nil, nil
		}

		// Sort if requested.
		if input.SortBy != "" {
			sortNodes(nodes, input.SortBy, input.BottomN > 0)
		}

		// Slice top_n / bottom_n.
		if input.TopN > 0 && input.TopN < len(nodes) {
			nodes = nodes[:input.TopN]
		} else if input.BottomN > 0 && input.BottomN < len(nodes) {
			nodes = nodes[len(nodes)-input.BottomN:]
		}

		type nodeWithIdx struct {
			*graph.Node
			Idx int `json:"idx,omitempty"`
		}
		enriched := make([]nodeWithIdx, len(nodes))
		for i, n := range nodes {
			enriched[i] = nodeWithIdx{Node: n, Idx: g.NodeIdx(n.ID)}
		}
		return toolJSON(enriched), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "query_edges",
		Description: "Query graph edges using a CEL expression. " +
			"Returns all edges where the expression is true. " +
			"Available fields: from, to, kind. " +
			"Call query_help for full documentation and examples.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input celInput) (*mcp.CallToolResult, any, error) {
		if input.Expr == "" {
			return toolError("expr is required"), nil, nil
		}
		edges, err := query.EdgeQuery(g, input.Expr)
		if err != nil {
			return toolError(fmt.Sprintf("query error: %v", err)), nil, nil
		}
		if len(edges) == 0 {
			return toolText("no matches"), nil, nil
		}
		return toolJSON(edges), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "query_help",
		Description: "Return the CEL query language documentation — available fields, operators, and examples for query_nodes and query_edges.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		return toolText(query.Help), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "tools_help",
		Description: "Return the complete azstral tool reference — all tools, their parameters, and what they do. Call this first when starting a new session.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		return toolText(toolsReference), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "query_examples",
		Description: "Return ready-to-use query examples grouped by use case: complexity, call graph, coverage, allocations, structure, vendor, edges.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		return toolText(query.Examples), nil, nil
	})
}

// --- Test and coverage tools ---

func registerTestTools(srv *mcp.Server, g *graph.Graph, root string) {
	type runTestsInput struct {
		Package string `json:"package,omitempty" jsonschema:"package pattern to test, e.g. './...' or './internal/executor'. Defaults to './...'"`
		Run     string `json:"run,omitempty" jsonschema:"optional -run regex to filter tests, e.g. 'TestParse'"`
		Dir     string `json:"dir,omitempty" jsonschema:"working directory for go test. Defaults to working root."`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "run_tests",
		Description: "Run go test with coverage and annotate graph nodes with results. " +
			"After calling this, query_nodes supports: " +
			"coverage (float 0-100), test_status ('pass'|'fail'|'covered'|'untested'). " +
			"Example queries: " +
			"kind==\"function\" && coverage < 50 — low coverage functions; " +
			"test_status == \"fail\" — failing tests; " +
			"kind==\"function\" && test_status == \"untested\" && cyclomatic > 10 — risky untested functions.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input runTestsInput) (*mcp.CallToolResult, any, error) {
		dir := input.Dir
		if dir == "" {
			dir = root
		}
		if dir == "" {
			return toolError("dir is required (no working root configured)"), nil, nil
		}

		res, err := testcov.Run(g, dir, input.Package, input.Run)
		if err != nil {
			// Non-fatal — partial results are still useful.
			return toolText(fmt.Sprintf(
				"tests: %d passed, %d failed, %d skipped — overall coverage: %.1f%% (warning: %v)",
				res.Passed, res.Failed, res.Skipped, res.Coverage, err,
			)), nil, nil
		}

		msg := fmt.Sprintf(
			"tests: %d passed, %d failed, %d skipped — overall coverage: %.1f%%",
			res.Passed, res.Failed, res.Skipped, res.Coverage,
		)
		if len(res.Failures) > 0 {
			msg += fmt.Sprintf("\nfailed tests:")
			for _, f := range res.Failures {
				msg += fmt.Sprintf("\n  FAIL %s/%s", f.Package, f.Test)
			}
		}
		msg += "\nGraph nodes annotated. Query with: test_status == \"fail\" or coverage < 50"
		return toolText(msg), nil, nil
	})
}

// --- Benchmark tools ---

func registerBenchTools(srv *mcp.Server, g *graph.Graph, root string) {
	type runBenchInput struct {
		Package string `json:"package,omitempty" jsonschema:"package pattern, e.g. './...' or './internal/executor'. Defaults to './...'"`
		Bench   string `json:"bench,omitempty" jsonschema:"benchmark name regex, e.g. 'BenchmarkRow' or '.' for all. Defaults to '.'"`
		Count   int    `json:"count,omitempty" jsonschema:"number of benchmark iterations (-count flag). 1 recommended. Defaults to Go default."`
		Dir     string `json:"dir,omitempty" jsonschema:"working directory. Defaults to working root."`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "run_bench",
		Description: "Run Go benchmarks and annotate matching Benchmark* function nodes with results. " +
			"After calling this, query_nodes supports: " +
			"bench_ns_op (float), bench_b_op (float), bench_allocs_op (float). " +
			"Example: kind==\"function\" && bench_ns_op > 1000 — slow benchmarks. " +
			"Example: kind==\"function\" && bench_allocs_op > 0 — benchmarks with allocations.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input runBenchInput) (*mcp.CallToolResult, any, error) {
		dir := input.Dir
		if dir == "" {
			dir = root
		}
		if dir == "" {
			return toolError("dir is required (no working root configured)"), nil, nil
		}

		sum, err := bench.Run(g, dir, input.Package, input.Bench, input.Count)
		if err != nil {
			return toolError(fmt.Sprintf("benchmark: %v", err)), nil, nil
		}

		if len(sum.Results) == 0 {
			return toolText("no benchmark results — check package and bench pattern"), nil, nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "ran %d benchmarks:\n", len(sum.Results))
		for _, r := range sum.Results {
			fmt.Fprintf(&b, "  %-40s %10.0f ns/op  %6.0f B/op  %4.0f allocs/op",
				r.Name, r.NsPerOp, r.BPerOp, r.AllocsPerOp)
			for unit, val := range r.Custom {
				fmt.Fprintf(&b, "  %.4g %s", val, unit)
			}
			b.WriteByte('\n')
		}
		if len(sum.Failures) > 0 {
			b.WriteString("failures: ")
			b.WriteString(strings.Join(sum.Failures, "; "))
			b.WriteByte('\n')
		}
		b.WriteString("Graph nodes annotated. Query with: bench_ns_op > 1000")
		return toolText(b.String()), nil, nil
	})

	type runProfileInput struct {
		Package   string `json:"package,omitempty" jsonschema:"package pattern. Defaults to './...'"`
		Bench     string `json:"bench,omitempty" jsonschema:"benchmark name regex. Defaults to '.'"`
		Type      string `json:"type,omitempty" jsonschema:"profile type: cpu (default), mem, block, mutex"`
		OutputDir string `json:"output_dir,omitempty" jsonschema:"directory to save the .pprof file. Defaults to os.TempDir()"`
		TopN      int    `json:"top_n,omitempty" jsonschema:"number of top functions to return. Defaults to 20"`
		Dir       string `json:"dir,omitempty" jsonschema:"working directory. Defaults to working root."`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "run_profile",
		Description: "Run a benchmark with pprof profiling, save the profile to disk, and annotate nodes. " +
			"Returns the profile file path (pass to 'go tool pprof' for interactive analysis) " +
			"plus a top-N function breakdown. " +
			"Annotates nodes with pprof_flat_pct and pprof_cum_pct for CEL queries.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input runProfileInput) (*mcp.CallToolResult, any, error) {
		dir := input.Dir
		if dir == "" {
			dir = root
		}
		if dir == "" {
			return toolError("dir is required (no working root configured)"), nil, nil
		}

		profType := bench.ProfileCPU
		switch strings.ToLower(input.Type) {
		case "mem", "memory":
			profType = bench.ProfileMem
		case "block":
			profType = bench.ProfileBlock
		case "mutex":
			profType = bench.ProfileMutex
		}

		res, err := bench.RunProfile(g, dir, input.Package, input.Bench, profType, input.OutputDir, input.TopN)
		if err != nil {
			return toolError(fmt.Sprintf("profile: %v", err)), nil, nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "profile saved: %s\n", res.Path)
		fmt.Fprintf(&b, "view interactively: go tool pprof %s\n\n", res.Path)
		if len(res.TopFuncs) > 0 {
			fmt.Fprintf(&b, "%-8s %-8s  %s\n", "flat%", "cum%", "function")
			for _, e := range res.TopFuncs {
				fmt.Fprintf(&b, "%-8.2f %-8.2f  %s\n", e.FlatPct, e.CumPct, e.Name)
			}
		}
		b.WriteString("\nGraph nodes annotated. Query with: pprof_flat_pct > 5.0")
		return toolText(b.String()), nil, nil
	})
}

// --- Hotspot analysis ---

func registerHotspotTools(srv *mcp.Server, g *graph.Graph, root string) {
	type findHotspotsInput struct {
		Package   string `json:"package,omitempty" jsonschema:"package pattern. Defaults to './...'"`
		Dir       string `json:"dir,omitempty" jsonschema:"working directory. Defaults to working root."`
		MinAllocs int    `json:"min_allocs,omitempty" jsonschema:"minimum heap_allocs to include. Defaults to 1."`
		TopN      int    `json:"top_n,omitempty" jsonschema:"max results. Defaults to 20."`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "find_hotspots",
		Description: "Run escape analysis then return the top heap-allocating non-external functions " +
			"with their full source text in one call. " +
			"Replaces the run_escape → query_nodes → get_nodes sequence. " +
			"Each result is a CCGF node block including body text ready for update_nodes.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input findHotspotsInput) (*mcp.CallToolResult, any, error) {
		dir := input.Dir
		if dir == "" {
			dir = root
		}
		if dir == "" {
			return toolError("dir is required (no working root configured)"), nil, nil
		}
		minAllocs := input.MinAllocs
		if minAllocs < 1 {
			minAllocs = 1
		}
		topN := input.TopN
		if topN <= 0 {
			topN = 20
		}

		res, err := escape.Run(g, dir, input.Package)
		if err != nil {
			return toolError(fmt.Sprintf("escape analysis: %v", err)), nil, nil
		}

		// Collect and rank non-external functions by heap_allocs.
		type entry struct {
			n     *graph.Node
			heaps int
		}
		var entries []entry
		for _, n := range g.NodesByKind(graph.KindFunction) {
			if n.Metadata["external"] == "true" {
				continue
			}
			heaps := res.HeapByFunc[n.ID]
			if heaps >= minAllocs {
				entries = append(entries, entry{n, heaps})
			}
		}
		// Sort by heap_allocs descending.
		for i := 1; i < len(entries); i++ {
			for j := i; j > 0 && entries[j].heaps > entries[j-1].heaps; j-- {
				entries[j], entries[j-1] = entries[j-1], entries[j]
			}
		}
		if len(entries) > topN {
			entries = entries[:topN]
		}
		if len(entries) == 0 {
			return toolText("no heap-allocating functions found — try a broader package pattern"), nil, nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "# %d hotspot functions (escape analysis: %d total, %d heap)\n\n",
			len(entries), res.Total, res.HeapTotal)
		for _, e := range entries {
			r := toolNode(e.n, root, g)
			b.WriteString(r.Content[0].(*mcp.TextContent).Text)
			fmt.Fprintf(&b, "  heap_allocs %d\n\n", e.heaps)
		}
		return toolText(strings.TrimSpace(b.String())), nil, nil
	})
}

// --- Escape analysis tools ---

func registerEscapeTools(srv *mcp.Server, g *graph.Graph, root string) {
	type escapeInput struct {
		Package string `json:"package,omitempty" jsonschema:"package pattern, e.g. './...' or './internal/executor'. Defaults to './...'"`
		Dir     string `json:"dir,omitempty" jsonschema:"working directory. Defaults to working root."`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "run_escape",
		Description: "Run Go escape analysis (go build -gcflags=-m) and annotate function nodes with allocation counts. " +
			"After calling this, query_nodes supports: " +
			"heap_allocs (int) — allocations that escape to heap; " +
			"stack_allocs (int) — allocations that stay on stack. " +
			"Example: kind==\"function\" && heap_allocs > 5 — hot allocating functions.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input escapeInput) (*mcp.CallToolResult, any, error) {
		dir := input.Dir
		if dir == "" {
			dir = root
		}
		if dir == "" {
			return toolError("dir is required (no working root configured)"), nil, nil
		}

		res, err := escape.Run(g, dir, input.Package)
		if err != nil {
			return toolError(fmt.Sprintf("escape analysis: %v", err)), nil, nil
		}

		return toolText(fmt.Sprintf(
			"escape analysis: %d total allocations, %d heap (%d%%) — graph nodes annotated with heap_allocs/stack_allocs",
			res.Total, res.HeapTotal,
			func() int {
				if res.Total == 0 {
					return 0
				}
				return res.HeapTotal * 100 / res.Total
			}(),
		)), nil, nil
	})
}

// --- Lint tools ---

func registerLintTools(srv *mcp.Server, g *graph.Graph, root string) {
	type runLintInput struct {
		Package string `json:"package,omitempty" jsonschema:"package pattern, e.g. './...' or './internal/executor'. Defaults to './...'"`
		Dir     string `json:"dir,omitempty" jsonschema:"working directory. Defaults to working root."`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "run_lint",
		Description: "Run golangci-lint and annotate function nodes with findings. " +
			"Uses .golangci.yml if present, otherwise runs a curated set of linters. " +
			"After calling this, query_nodes supports: " +
			"lint_count (int) — number of issues on this node; " +
			"lint_issues (string) — semicolon-separated 'linter: message' list. " +
			"Requires golangci-lint to be installed.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input runLintInput) (*mcp.CallToolResult, any, error) {
		dir := input.Dir
		if dir == "" {
			dir = root
		}
		if dir == "" {
			return toolError("dir is required (no working root configured)"), nil, nil
		}

		res, err := lint.Run(g, dir, input.Package)
		if err != nil {
			return toolError(fmt.Sprintf("lint: %v", err)), nil, nil
		}
		if res.Total == 0 {
			return toolText("no lint issues found"), nil, nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "found %d issue(s) from linters: %s\n", res.Total, strings.Join(res.Linters, ", "))
		fmt.Fprintf(&b, "annotated %d function node(s)\n", len(res.ByNode))
		b.WriteString("Query with: lint_count > 0  or  lint_issues.contains(\"errcheck\")")
		return toolText(b.String()), nil, nil
	})
}

// --- Race and deadlock detection ---

func registerRaceTools(srv *mcp.Server, g *graph.Graph) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "find_races",
		Description: "Statically analyse the graph for common concurrency anti-patterns. " +
			"Detects: goroutine loop variable capture (HIGH), mutex Lock without deferred Unlock (MEDIUM), " +
			"channel sends inside loops (LOW), shared variable accessed from goroutine and main scope " +
			"without an observable mutex (HIGH). " +
			"NOTE: heuristic only — use 'go test -race' for definitive race detection. " +
			"Requires parse_tree to have been called first.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		issues := races.Analyze(g)
		if len(issues) == 0 {
			return toolText("no concurrency issues found"), nil, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "found %d potential concurrency issue(s):\n\n", len(issues))
		for _, iss := range issues {
			fmt.Fprintf(&b, "[%s] %s\n", iss.Severity, iss.Kind)
			if iss.File != "" {
				file := iss.File
				if len(file) > 60 {
					file = "…" + file[len(file)-57:]
				}
				fmt.Fprintf(&b, "  %s:%d\n", file, iss.Line)
			}
			fmt.Fprintf(&b, "  %s\n\n", iss.Message)
		}
		return toolText(strings.TrimSpace(b.String())), nil, nil
	})
}

// --- Combined analysis ---

func registerAnalysisTools(srv *mcp.Server, g *graph.Graph, root string) {
	type runAllInput struct {
		Package string `json:"package,omitempty" jsonschema:"package pattern. Defaults to './...'"`
		Dir     string `json:"dir,omitempty" jsonschema:"working directory. Defaults to working root."`
		Tests   bool   `json:"tests,omitempty" jsonschema:"run go test with coverage (slow). Default false."`
		Bench   string `json:"bench,omitempty" jsonschema:"benchmark name regex to run (e.g. 'BenchmarkFoo'). Empty skips benchmarks."`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "run_analysis",
		Description: "Run all static analysis in one call: escape analysis, golangci-lint, " +
			"and concurrency pattern detection. Optionally also runs tests and benchmarks. " +
			"Annotates nodes so you can query: " +
			"lint_count, lint_issues, race_count, race_issues, heap_allocs, " +
			"coverage, test_status, bench_allocs_op. " +
			"Returns a summary with the riskiest functions.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input runAllInput) (*mcp.CallToolResult, any, error) {
		dir := input.Dir
		if dir == "" {
			dir = root
		}
		if dir == "" {
			return toolError("dir is required (no working root configured)"), nil, nil
		}

		var b strings.Builder
		var totalIssues int

		// 1. Escape analysis.
		b.WriteString("## Escape analysis\n")
		escRes, err := escape.Run(g, dir, input.Package)
		if err != nil {
			fmt.Fprintf(&b, "  warning: %v\n", err)
		} else {
			fmt.Fprintf(&b, "  %d total allocations, %d heap\n", escRes.Total, escRes.HeapTotal)
			totalIssues += escRes.HeapTotal
		}

		// 2. Lint.
		b.WriteString("## Lint\n")
		lintRes, err := lint.Run(g, dir, input.Package)
		if err != nil {
			fmt.Fprintf(&b, "  warning: %v\n", err)
		} else {
			fmt.Fprintf(&b, "  %d issue(s) from: %s\n", lintRes.Total, strings.Join(lintRes.Linters, ", "))
			totalIssues += lintRes.Total
		}

		// 3. Race detection.
		b.WriteString("## Concurrency patterns\n")
		raceIssues := races.Analyze(g)
		fmt.Fprintf(&b, "  %d issue(s) found\n", len(raceIssues))
		totalIssues += len(raceIssues)
		for _, iss := range raceIssues {
			if iss.Severity == "HIGH" {
				fmt.Fprintf(&b, "  [HIGH] %s: %s\n", iss.Kind, iss.Message)
			}
		}

		// 4. Tests (optional).
		if input.Tests {
			b.WriteString("## Tests\n")
			testRes, err := testcov.Run(g, dir, input.Package, "")
			if err != nil {
				fmt.Fprintf(&b, "  warning: %v\n", err)
			} else {
				fmt.Fprintf(&b, "  %d passed, %d failed — %.1f%% coverage\n",
					testRes.Passed, testRes.Failed, testRes.Coverage)
				totalIssues += testRes.Failed
			}
		}

		// 5. Benchmarks (optional).
		if input.Bench != "" {
			b.WriteString("## Benchmarks\n")
			benchRes, err := bench.Run(g, dir, input.Package, input.Bench, 1)
			if err != nil {
				fmt.Fprintf(&b, "  warning: %v\n", err)
			} else {
				fmt.Fprintf(&b, "  %d benchmark(s) annotated\n", len(benchRes.Results))
			}
		}

		// Summary: top risky functions across all dimensions.
		b.WriteString("\n## Top risk functions\n")
		b.WriteString("Query: query_nodes expr=\"lint_count > 0 || race_count > 0 || heap_allocs > 3\" sort_by=\"lint_count\" top_n=10\n")
		fmt.Fprintf(&b, "\nTotal issues across all analyses: %d\n", totalIssues)
		b.WriteString("All nodes annotated — use query_nodes to explore.\n")

		return toolText(b.String()), nil, nil
	})
}

// --- Graph management tools ---

func registerGraphTools(srv *mcp.Server, g *graph.Graph, root string) {
	desc := "Clear the in-memory graph and re-parse from the working root. " +
		"Use this when the graph has become stale or contaminated from parsing multiple projects."
	if root != "" {
		desc += fmt.Sprintf(" Working root: %s.", root)
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "reset_graph",
		Description: desc,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		g.Reset()
		if root == "" {
			return toolText("graph cleared (no working root to re-parse)"), nil, nil
		}
		n, err := parser.ParseTree(g, root)
		if err != nil {
			return toolError(fmt.Sprintf("re-parse error: %v", err)), nil, nil
		}
		return toolText(fmt.Sprintf("graph reset — parsed %d files, %d nodes from %s", n, len(g.Nodes), root)), nil, nil
	})
}

// sortNodes sorts nodes by a named field. ascending=false means descending (highest first).
// Numeric fields (cyclomatic, cognitive, coverage, heap_allocs, stack_allocs,
// bench_ns_op, bench_b_op, bench_allocs_op, pprof_flat_pct, pprof_cum_pct, line)
// are sorted numerically. Everything else sorts lexicographically.
func sortNodes(nodes []*graph.Node, field string, ascending bool) {
	getFloat := func(n *graph.Node) float64 {
		switch field {
		case "cyclomatic":
			v, _ := strconv.ParseFloat(n.Metadata["cyclomatic"], 64)
			return v
		case "cognitive":
			v, _ := strconv.ParseFloat(n.Metadata["cognitive"], 64)
			return v
		case "coverage":
			v, _ := strconv.ParseFloat(n.Metadata["coverage"], 64)
			return v
		case "heap_allocs":
			v, _ := strconv.ParseFloat(n.Metadata["heap_allocs"], 64)
			return v
		case "stack_allocs":
			v, _ := strconv.ParseFloat(n.Metadata["stack_allocs"], 64)
			return v
		case "bench_ns_op":
			v, _ := strconv.ParseFloat(n.Metadata["bench_ns_op"], 64)
			return v
		case "bench_b_op":
			v, _ := strconv.ParseFloat(n.Metadata["bench_b_op"], 64)
			return v
		case "bench_allocs_op":
			v, _ := strconv.ParseFloat(n.Metadata["bench_allocs_op"], 64)
			return v
		case "pprof_flat_pct":
			v, _ := strconv.ParseFloat(n.Metadata["pprof_flat_pct"], 64)
			return v
		case "pprof_cum_pct":
			v, _ := strconv.ParseFloat(n.Metadata["pprof_cum_pct"], 64)
			return v
		case "line":
			return float64(n.Line)
		case "callee_count":
			// Count callee edges dynamically — not stored in metadata.
			return float64(0) // handled by string path below
		}
		return math.NaN() // fall through to string sort
	}
	getString := func(n *graph.Node) string {
		switch field {
		case "name":
			return n.Name
		case "kind":
			return string(n.Kind)
		case "file":
			return n.File
		case "id":
			return n.ID
		}
		return n.Metadata[field] // arbitrary metadata key
	}

	sort.SliceStable(nodes, func(i, j int) bool {
		fi, fj := getFloat(nodes[i]), getFloat(nodes[j])
		if !math.IsNaN(fi) && !math.IsNaN(fj) {
			if ascending {
				return fi < fj
			}
			return fi > fj
		}
		si, sj := getString(nodes[i]), getString(nodes[j])
		if ascending {
			return si < sj
		}
		return si > sj
	})
}


// toolsReference is the complete tool reference returned by tools_help.
const toolsReference = `# Azstral Tool Reference

## Quick start

  tools_help          — this reference
  query_help          — CEL query field reference
  query_examples      — ready-to-use query examples
  ccgf_grammar        — CCGF format spec

## Parse / load

  parse_tree(path?)          — parse a project tree, reset graph. Defaults to working root.
  parse_files(paths[])       — parse specific files (additive)
  parse_packages(path?)      — type-check + parse (slower, adds qualified_id + variable dict)
  reset_graph()              — clear graph and re-parse from root

## Query

  get_graph()                — full graph as JSON
  get_nodes(ids[])           — fetch nodes by ID, returns CCGF with body text
  list_nodes(kind?)          — list nodes, optionally by kind
  list_edges(from?, to?)     — list edges
  query_nodes(expr)          — CEL expression filter over nodes
  query_edges(expr)          — CEL expression filter over edges
  encode_ccgf(scope?,attrs?) — compact structural overview

## Analysis

  find_hotspots(package?, min_allocs?, top_n?)  — escape analysis + return top alloc functions with body
  run_tests(package?, run?, dir?)          — go test + coverage; annotates nodes with coverage/test_status
  run_escape(package?, dir?)              — escape analysis; annotates heap_allocs/stack_allocs
  run_bench(package?, bench?, count?)     — go test -bench; annotates bench_ns_op/bench_b_op/bench_allocs_op
  run_profile(package?, bench?, type?)    — pprof profile; returns file path + top-N; annotates pprof_flat_pct
  run_vet(package?, dir?)                 — go vet; annotates nodes with vet_issues metadata
  find_todos(pattern?)                    — find TODO/FIXME/HACK/XXX in comments (pattern is pipe-separated)

## Call graph

  call_graph(id, depth?, direction?)      — BFS over call graph; depth default 3; direction: callees|callers|both
  call_path(from_id, to_id)              — shortest call path between two functions

## Implementations

  find_implementations(interface_id)      — find types that implement an interface

## Graph snapshots

  graph_snapshot(name?)                   — snapshot current node set to memory
  graph_diff(snapshot_name)              — diff current graph vs snapshot: added/deleted/changed

## Specs

  get_spec(id)
  list_specs(kind?, namespace?)
  find_spec(id)

## Docs

  render(id)           — preview a file as Go source (read-only)
  ccgf_grammar()       — CCGF format definition
  query_help()         — CEL fields + operators + examples
  query_examples()     — curated queries by use case
  tools_help()         — this reference

## Node kinds

  Structural:  package, file, function, type, variable, import, comment, spec
  Statements:  for, if, switch, select, return, defer, go, assign, send, branch, statement
  Expressions: expr:binary, expr:unary, expr:ident, expr:selector, expr:index,
               expr:literal, expr:composite, expr:typeassert, expr:func
  Variables:   local (from parse_packages — type-checked variable dictionary)

## Node ID format

  pkg:pkgname                         package
  file:/abs/path/to/file.go           file
  func:pkgname.Name                   package-level function
  func:pkgname.RecvType.Name          method
  type:pkgname.Name                   type
  var:pkgname.Name                    package-level variable
  field:pkgname.TypeName.FieldName    struct field
  for:file:/path:42                   for loop at line 42
  if:file:/path:45                    if statement
  assign:file:/path:50               assignment
  expr:bin:file:/path:45:10          binary expression at line 45 col 10
  local:pkg.FuncName.varname:15      local variable (parse_packages only)

## CEL query fields (summary — see query_help for full reference)

  kind, name, id, file, line, text, external, parent_id
  cyclomatic, cognitive                      complexity
  callee_ids, caller_ids, child_ids          call graph
  coverage, test_status                      from run_tests
  heap_allocs, stack_allocs                  from run_escape
  bench_ns_op, bench_b_op, bench_allocs_op   from run_bench
  pprof_flat_pct, pprof_cum_pct              from run_profile
  metadata.num("key")                        any numeric metadata field
`
