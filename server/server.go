// Package server provides the MCP server for azstral.
// SPEC-003: Expose the code graph via an MCP server.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/matt/azstral/ccgf"
	"github.com/matt/azstral/codegen"
	"github.com/matt/azstral/edit"
	"github.com/matt/azstral/graph"
	"github.com/matt/azstral/parser"
	"github.com/matt/azstral/query"
	"github.com/matt/azstral/store"
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
			return nil, fmt.Errorf("parse root %s: %w", root, perr)
		}
		parseMsg = fmt.Sprintf(" Parsed %d files from %s.", n, root)
	}

	instructions := "Azstral represents Go code as a connected graph. " +
		"The graph is already loaded — use encode_ccgf or query_nodes to explore it. " +
		"Modify code with update_node/add_node/add_edge, then write_file to persist." +
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
	registerQueryTools(srv, g)
	registerMutationTools(srv, g)
	registerSpecTools(srv, g, st)
	registerCodegenTools(srv, g, st)
	registerCCGFTools(srv, g)
	registerCELTools(srv, g)
	registerGraphTools(srv, g, root)
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
		Name:        "parse_file",
		Description: "Parse a Go source file and add its AST to the graph.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		if input.Path == "" {
			return toolError("path is required"), nil, nil
		}
		if err := parser.ParseFile(g, input.Path); err != nil {
			return toolError(fmt.Sprintf("parse error: %v", err)), nil, nil
		}
		return toolText(fmt.Sprintf("parsed %s — %d nodes", input.Path, len(g.Nodes))), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "parse_dir",
		Description: "Parse all Go files in a directory.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input pathInput) (*mcp.CallToolResult, any, error) {
		if input.Path == "" {
			return toolError("path is required"), nil, nil
		}
		if err := parser.ParseDir(g, input.Path); err != nil {
			return toolError(fmt.Sprintf("parse error: %v", err)), nil, nil
		}
		return toolText(fmt.Sprintf("parsed dir %s — %d nodes", input.Path, len(g.Nodes))), nil, nil
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
func registerQueryTools(srv *mcp.Server, g *graph.Graph) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_graph",
		Description: "Return the full code graph as JSON.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		return toolJSON(g), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_node",
		Description: "Return a single graph node by ID.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input nodeIDInput) (*mcp.CallToolResult, any, error) {
		node, ok := g.GetNode(input.ID)
		if !ok {
			return toolError(fmt.Sprintf("node %q not found", input.ID)), nil, nil
		}
		return toolNode(node), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_nodes",
		Description: "List graph nodes, optionally filtered by kind.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input listNodesInput) (*mcp.CallToolResult, any, error) {
		if input.Kind != "" {
			return toolJSON(g.NodesByKind(graph.NodeKind(input.Kind))), nil, nil
		}
		return toolJSON(g.Nodes), nil, nil
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

// --- Mutation tools ---
func registerMutationTools(srv *mcp.Server, g *graph.Graph) {
	type addNodeInput struct {
		ID       string            `json:"id" jsonschema:"unique node ID, e.g. pkg:main, file:main.go, func:main"`
		Kind     string            `json:"kind" jsonschema:"node kind: package, file, function, type, variable, comment, import, statement"`
		Name     string            `json:"name" jsonschema:"node name (package name, function name, import path, etc.)"`
		Text     string            `json:"text,omitempty" jsonschema:"text content: function body, statement code, comment text, type definition"`
		File     string            `json:"file,omitempty" jsonschema:"source file path this node belongs to"`
		Line     int               `json:"line,omitempty" jsonschema:"line number for ordering within parent"`
		Metadata map[string]string `json:"metadata,omitempty" jsonschema:"key-value metadata: alias, params, returns, receiver, const, type"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_node",
		Description: "Add a node to the code graph. Use kind to specify what it represents (package, file, function, import, statement, etc.).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input addNodeInput) (*mcp.CallToolResult, any, error) {
		n := &graph.Node{
			ID:       input.ID,
			Kind:     graph.NodeKind(input.Kind),
			Name:     input.Name,
			Text:     input.Text,
			File:     input.File,
			Line:     input.Line,
			Metadata: input.Metadata,
		}
		if err := g.AddNode(n); err != nil {
			return toolError(err.Error()), nil, nil
		}
		// Sync to disk based on node kind.
		switch n.Kind {
		case graph.KindFile:
			// Create the physical file with a package declaration.
			filePath := strings.TrimPrefix(n.ID, "file:")
			if filePath != n.ID { // only if ID has file: prefix
				pkg := n.Metadata["package"]
				if pkg == "" {
					pkg = filepath.Base(filepath.Dir(filePath))
				}
				if err := createGoFile(filePath, pkg); err != nil {
					return toolText(fmt.Sprintf("added %s — warning: %v", n.ID, err)), nil, nil
				}
			}
		case graph.KindFunction:
			// Append function to its file.
			if n.File != "" && n.Text != "" {
				filePath := strings.TrimPrefix(n.File, "file:")
				params := n.Metadata["params"]
				returns := n.Metadata["returns"]
				receiver := n.Metadata["receiver"]
				if err := edit.AppendFunction(filePath, n.Name, receiver, params, returns, n.Text); err != nil {
					return toolText(fmt.Sprintf("added %s — warning: %v", n.ID, err)), nil, nil
				}
			}
		}
		return toolNode(n), nil, nil
	})

	type addNodesInput struct {
		Nodes []addNodeInput `json:"nodes" jsonschema:"array of nodes to add"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_nodes",
		Description: "Add multiple nodes to the code graph in a single call. Returns count of nodes added and any errors.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input addNodesInput) (*mcp.CallToolResult, any, error) {
		var errs []string
		added := 0
		for _, item := range input.Nodes {
			n := &graph.Node{
				ID:       item.ID,
				Kind:     graph.NodeKind(item.Kind),
				Name:     item.Name,
				Text:     item.Text,
				File:     item.File,
				Line:     item.Line,
				Metadata: item.Metadata,
			}
			if err := g.AddNode(n); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", item.ID, err))
			} else {
				added++
			}
		}
		msg := fmt.Sprintf("added %d/%d nodes", added, len(input.Nodes))
		if len(errs) > 0 {
			msg += "; errors: " + strings.Join(errs, "; ")
		}
		return toolText(msg), nil, nil
	})

	type updateNodeInput struct {
		ID       string            `json:"id" jsonschema:"node ID to update"`
		Name     *string           `json:"name,omitempty" jsonschema:"new name"`
		Text     *string           `json:"text,omitempty" jsonschema:"new text content (function body, statement code, etc.)"`
		File     *string           `json:"file,omitempty" jsonschema:"new file path"`
		Line     *int              `json:"line,omitempty" jsonschema:"new line number"`
		EndLine  *int              `json:"end_line,omitempty" jsonschema:"new end line"`
		Metadata map[string]string `json:"metadata,omitempty" jsonschema:"metadata keys to set or update"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "update_node",
		Description: "Update fields on an existing graph node. Only provided fields are changed; omitted fields are left as-is.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input updateNodeInput) (*mcp.CallToolResult, any, error) {
		patch := graph.NodePatch{
			Name:     input.Name,
			Text:     input.Text,
			File:     input.File,
			Line:     input.Line,
			EndLine:  input.EndLine,
			Metadata: input.Metadata,
		}
		if err := g.UpdateNode(input.ID, patch); err != nil {
			return toolError(err.Error()), nil, nil
		}
		node, _ := g.GetNode(input.ID)
		// Sync function body changes directly to disk — no write_file needed.
		if input.Text != nil && node.Kind == graph.KindFunction && node.File != "" {
			filePath := strings.TrimPrefix(node.File, "file:")
			if err := edit.FunctionBody(filePath, node.Name, node.Metadata["receiver"], *input.Text); err != nil {
				// Non-fatal: graph is updated, file patch failed. Return warning.
				return toolText(fmt.Sprintf("updated %s — warning: %v", node.ID, err)), nil, nil
			}
		}
		return toolNode(node), nil, nil
	})

	type addEdgeInput struct {
		From string `json:"from" jsonschema:"source node ID"`
		To   string `json:"to" jsonschema:"target node ID"`
		Kind string `json:"kind" jsonschema:"edge kind: contains, calls, references, annotates, covers"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_edge",
		Description: "Add a directed edge between two nodes. Use 'contains' for parent-child (package→file, file→function), 'calls' for call relationships, 'annotates' for comment→code.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input addEdgeInput) (*mcp.CallToolResult, any, error) {
		if err := g.AddEdge(input.From, input.To, graph.EdgeKind(input.Kind)); err != nil {
			return toolError(err.Error()), nil, nil
		}
		return toolText(fmt.Sprintf("added edge %s -[%s]-> %s", input.From, input.Kind, input.To)), nil, nil
	})

	type addEdgesInput struct {
		Edges []addEdgeInput `json:"edges" jsonschema:"array of edges to add"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_edges",
		Description: "Add multiple edges to the code graph in a single call. Returns count of edges added and any errors.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input addEdgesInput) (*mcp.CallToolResult, any, error) {
		var errs []string
		added := 0
		for _, item := range input.Edges {
			if err := g.AddEdge(item.From, item.To, graph.EdgeKind(item.Kind)); err != nil {
				errs = append(errs, fmt.Sprintf("%s→%s: %v", item.From, item.To, err))
			} else {
				added++
			}
		}
		msg := fmt.Sprintf("added %d/%d edges", added, len(input.Edges))
		if len(errs) > 0 {
			msg += "; errors: " + strings.Join(errs, "; ")
		}
		return toolText(msg), nil, nil
	})

	type deleteEdgeInput struct {
		From string `json:"from" jsonschema:"source node ID"`
		To   string `json:"to" jsonschema:"target node ID"`
		Kind string `json:"kind" jsonschema:"edge kind: contains, calls, callee, references, annotates, covers"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "delete_edge",
		Description: "Remove a directed edge between two nodes. Useful for correcting spurious edges before rendering a file.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input deleteEdgeInput) (*mcp.CallToolResult, any, error) {
		if err := g.RemoveEdge(input.From, input.To, graph.EdgeKind(input.Kind)); err != nil {
			return toolError(err.Error()), nil, nil
		}
		return toolText(fmt.Sprintf("removed edge %s -[%s]-> %s", input.From, input.Kind, input.To)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "delete_edges",
		Description: "Remove multiple edges in a single call. Returns count removed and any errors.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input addEdgesInput) (*mcp.CallToolResult, any, error) {
		var errs []string
		removed := 0
		for _, item := range input.Edges {
			if err := g.RemoveEdge(item.From, item.To, graph.EdgeKind(item.Kind)); err != nil {
				errs = append(errs, fmt.Sprintf("%s→%s: %v", item.From, item.To, err))
			} else {
				removed++
			}
		}
		msg := fmt.Sprintf("removed %d/%d edges", removed, len(input.Edges))
		if len(errs) > 0 {
			msg += "; errors: " + strings.Join(errs, "; ")
		}
		return toolText(msg), nil, nil
	})
}

// --- Spec store tools ---
func registerSpecTools(srv *mcp.Server, g *graph.Graph, st *store.Store) {
	type createSpecInput struct {
		ID        string `json:"id" jsonschema:"globally unique spec ID, e.g. SPEC-001, NOTE-003, TEST-006, BENCH-001"`
		Kind      string `json:"kind" jsonschema:"SPEC, NOTE, TEST, or BENCH"`
		Namespace string `json:"namespace,omitempty" jsonschema:"namespace scope, e.g. io, graph, codegen. Empty for root."`
		Title     string `json:"title" jsonschema:"short title describing the spec"`
		Body      string `json:"body,omitempty" jsonschema:"full description"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "create_spec",
		Description: "Create a spec/note/test/benchmark in the database. IDs are globally unique but scoped by namespace.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input createSpecInput) (*mcp.CallToolResult, any, error) {
		sp := &store.Spec{
			ID:        strings.ToUpper(input.ID),
			Kind:      strings.ToUpper(input.Kind),
			Namespace: input.Namespace,
			Title:     input.Title,
			Body:      input.Body,
		}
		if err := st.CreateSpec(sp); err != nil {
			return toolError(err.Error()), nil, nil
		}
		return toolText(fmt.Sprintf("created %s in namespace %q", sp.ID, sp.Namespace)), nil, nil
	})

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

	type linkSpecInput struct {
		SpecID string `json:"spec_id" jsonschema:"spec ID to link, e.g. SPEC-004"`
		NodeID string `json:"node_id" jsonschema:"graph node ID to link to, e.g. func:main"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "link_spec",
		Description: "Link a spec to a graph node. This creates a coverage relationship — the spec applies to that code.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input linkSpecInput) (*mcp.CallToolResult, any, error) {
		if err := st.LinkSpec(strings.ToUpper(input.SpecID), input.NodeID); err != nil {
			return toolError(err.Error()), nil, nil
		}
		return toolText(fmt.Sprintf("linked %s → %s", input.SpecID, input.NodeID)), nil, nil
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

// toolNode renders a single graph node as CCGF text using the node's own ID
// as the symbol identifier. Output uses the same format as encode_ccgf.
func toolNode(n *graph.Node) *mcp.CallToolResult {
	if n == nil {
		return toolError("node is nil")
	}

	// Map kind to CCGF type code.
	typeCode := "v"
	switch n.Kind {
	case graph.KindPackage:
		typeCode = "p"
	case graph.KindFunction:
		if n.Metadata["receiver"] != "" {
			typeCode = "m"
		} else {
			typeCode = "f"
		}
	case graph.KindType:
		if strings.Contains(n.Text, "interface") {
			typeCode = "i"
		} else {
			typeCode = "t"
		}
	case graph.KindVariable:
		if n.Metadata["const"] == "true" {
			typeCode = "c"
		}
	}

	id := n.ID
	var b strings.Builder
	fmt.Fprintf(&b, "s %s %s %s\n", id, typeCode, n.Name)

	// Attributes.
	if n.File != "" {
		loc := n.File
		if n.Line > 0 {
			loc = fmt.Sprintf("%s:%d", n.File, n.Line)
		}
		fmt.Fprintf(&b, "a %s loc %s\n", id, loc)
	}
	if sig := buildNodeSig(n); sig != "" {
		fmt.Fprintf(&b, "a %s sig %s\n", id, sig)
	}
	if cyc := n.Metadata["cyclomatic"]; cyc != "" && cyc != "0" && cyc != "1" {
		fmt.Fprintf(&b, "a %s cyclo %s\n", id, cyc)
	}
	if cog := n.Metadata["cognitive"]; cog != "" && cog != "0" {
		fmt.Fprintf(&b, "a %s cogn %s\n", id, cog)
	}
	if n.Metadata["external"] == "true" {
		fmt.Fprintf(&b, "a %s ro 1\n", id)
	}

	return toolText(strings.TrimRight(b.String(), "\n"))
}

func buildNodeSig(n *graph.Node) string {
	if n.Kind != graph.KindFunction {
		return ""
	}
	var sig strings.Builder
	sig.WriteString("func")
	if recv := n.Metadata["receiver"]; recv != "" {
		fmt.Fprintf(&sig, "(%s)", recv)
	}
	params := n.Metadata["params"]
	sig.WriteByte('(')
	sig.WriteString(params)
	sig.WriteByte(')')
	if ret := n.Metadata["returns"]; ret != "" {
		sig.WriteString(ret)
	}
	return sig.String()
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

// createGoFile creates a new Go source file with just the package declaration.
// Only operates on absolute paths — relative paths are ignored to prevent
// accidental file creation in the server's working directory.
func createGoFile(path, pkg string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("createGoFile requires absolute path, got %q", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil // already exists — don't overwrite
	}
	return os.WriteFile(path, []byte("package "+pkg+"\n"), 0o644)
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
func registerCCGFTools(srv *mcp.Server, g *graph.Graph) {
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

func registerCELTools(srv *mcp.Server, g *graph.Graph) {
	type celInput struct {
		Expr string `json:"expr" jsonschema:"CEL expression to evaluate against each node or edge. Call query_help for available fields and examples."`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "query_nodes",
		Description: "Query graph nodes using a CEL expression. " +
			"Returns all nodes where the expression is true. " +
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
		return toolJSON(nodes), nil, nil
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
