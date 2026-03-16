// Package server provides the MCP server for azstral.
// SPEC-003: Expose the code graph via an MCP server.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/matt/azstral/ccgf"
	"github.com/matt/azstral/codegen"
	"github.com/matt/azstral/edit"
	"github.com/matt/azstral/graph"
	"github.com/matt/azstral/parser"
	"github.com/matt/azstral/query"
	"github.com/matt/azstral/bench"
	"github.com/matt/azstral/escape"
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
	registerTestTools(srv, g, root)
	registerEscapeTools(srv, g, root)
	registerBenchTools(srv, g, root)
	registerHotspotTools(srv, g, root)
	registerDeleteTools(srv, g)
	registerImportTools(srv, g)
	registerCallGraphTools(srv, g)
	registerFindImplementations(srv, g)
	registerVetTools(srv, g, root)
	registerFindTodos(srv, g)
	registerRefactorTools(srv, g)
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
func registerQueryTools(srv *mcp.Server, g *graph.Graph) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_graph",
		Description: "Return the full code graph as JSON.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
		return toolJSON(g), nil, nil
	})

	type getNodesInput struct {
		IDs []string `json:"ids" jsonschema:"array of node IDs to retrieve"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_nodes",
		Description: "Return multiple graph nodes by ID in a single call. Returns CCGF lines for each found node.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input getNodesInput) (*mcp.CallToolResult, any, error) {
		var b strings.Builder
		var missing []string
		for _, id := range input.IDs {
			node, ok := g.GetNode(id)
			if !ok {
				missing = append(missing, id)
				continue
			}
			// Reuse toolNode's text but concatenate all.
			r := toolNode(node)
			b.WriteString(r.Content[0].(*mcp.TextContent).Text)
			b.WriteString("\n\n")
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
		File     string            `json:"file,omitempty" jsonschema:"source file path this node belongs to (file: node ID)"`
		Line     int               `json:"line,omitempty" jsonschema:"line number for ordering within parent"`
		Metadata map[string]string `json:"metadata,omitempty" jsonschema:"key-value metadata: alias, params, returns, receiver, const, type, package, insert_after"`
	}

	syncNodeToDisk := func(n *graph.Node) string {
		switch n.Kind {
		case graph.KindFile:
			filePath := strings.TrimPrefix(n.ID, "file:")
			if filePath != n.ID {
				pkg := n.Metadata["package"]
				if pkg == "" {
					pkg = filepath.Base(filepath.Dir(filePath))
				}
				if err := createGoFile(filePath, pkg); err != nil {
					return err.Error()
				}
			}
		case graph.KindFunction:
			if n.File != "" && n.Text != "" {
				filePath := strings.TrimPrefix(n.File, "file:")
				if err := edit.AppendFunction(filePath, n.Name, n.Metadata["receiver"],
					n.Metadata["params"], n.Metadata["returns"], n.Text); err != nil {
					return err.Error()
				}
			}
		}
		return ""
	}

	// insertStmtNode handles the insert_after logic for statement nodes.
	// It connects the new node into the graph at the right position and
	// regenerates the enclosing function body on disk.
	insertStmtNode := func(n *graph.Node) string {
		insertAfter := n.Metadata["insert_after"]
		if insertAfter == "" {
			return ""
		}

		// Find the parent of the insert_after node (could be a function or another statement).
		parentID := ""
		var afterNode *graph.Node
		afterNode, _ = g.GetNode(insertAfter)
		if afterNode == nil {
			return fmt.Sprintf("insert_after node %q not found", insertAfter)
		}

		// Find the containing parent via EdgeContains.
		for _, e := range g.EdgesTo(insertAfter) {
			if e.Kind == graph.EdgeContains {
				parentID = e.From
				break
			}
		}
		if parentID == "" {
			// insert_after is the function itself — attach directly to it.
			parentID = insertAfter
		}

		// Compute a line number just after the insert_after node.
		refLine := afterNode.EndLine
		if refLine == 0 {
			refLine = afterNode.Line
		}
		if n.Line == 0 {
			n.Line = refLine + 1
		}

		// Shift existing sibling lines to make room.
		children := g.Children(parentID)
		for _, sibling := range children {
			if sibling.ID != n.ID && sibling.Line >= n.Line {
				newLine := sibling.Line + 1
				newEnd := sibling.EndLine
				if newEnd > 0 {
					newEnd = sibling.EndLine + 1
				}
				_ = g.UpdateNode(sibling.ID, graph.NodePatch{Line: &newLine, EndLine: &newEnd})
			}
		}

		// Connect the node to its parent.
		_ = g.AddEdge(parentID, n.ID, graph.EdgeContains)

		// Propagate file from parent.
		if n.File == "" {
			if parent, ok := g.GetNode(parentID); ok && parent.File != "" {
				n.File = parent.File
			}
		}

		// Regenerate the enclosing function body.
		funcID, fp, funcName, receiver, ok := findEnclosingFunc(g, n.ID)
		if !ok {
			// parentID might itself be the function.
			if parent, exists := g.GetNode(parentID); exists && parent.Kind == graph.KindFunction {
				funcID = parentID
				if parent.File != "" {
					fp = strings.TrimPrefix(parent.File, "file:")
				}
				funcName = parent.Name
				receiver = parent.Metadata["receiver"]
				ok = true
			}
		}
		if ok {
			if body, hasBody := codegen.RenderBody(g, funcID); hasBody {
				if err := edit.FunctionBody(fp, funcName, receiver, body); err != nil {
					return err.Error()
				}
			}
		}
		return ""
	}

	type addNodesInput struct {
		Nodes []addNodeInput `json:"nodes" jsonschema:"array of nodes to add. kind=file creates the file on disk; kind=function appends to its file. Statement nodes with insert_after metadata are inserted after the specified node."`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_nodes",
		Description: "Add nodes to the code graph. Automatically syncs to disk: file nodes create the .go file, function nodes append to their file. Statement nodes (for/if/assign/etc.) with an insert_after metadata key are inserted after the specified node ID and the enclosing function body is regenerated.",
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
				continue
			}
			if isStmtNode(n.Kind) && n.Metadata["insert_after"] != "" {
				if warn := insertStmtNode(n); warn != "" {
					errs = append(errs, fmt.Sprintf("%s (insert): %s", item.ID, warn))
				}
			} else {
				if warn := syncNodeToDisk(n); warn != "" {
					errs = append(errs, fmt.Sprintf("%s (disk): %s", item.ID, warn))
				}
			}
			added++
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

	type updateNodesInput struct {
		Nodes []updateNodeInput `json:"nodes" jsonschema:"array of node updates to apply"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "update_nodes",
		Description: "Update multiple graph nodes in a single call. Each entry uses the same fields as update_node. Returns CCGF lines for each updated node.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input updateNodesInput) (*mcp.CallToolResult, any, error) {
		var lines []string
		var errs []string
		for _, item := range input.Nodes {
			patch := graph.NodePatch{
				Name:     item.Name,
				Text:     item.Text,
				File:     item.File,
				Line:     item.Line,
				EndLine:  item.EndLine,
				Metadata: item.Metadata,
			}
			if err := g.UpdateNode(item.ID, patch); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", item.ID, err))
				continue
			}
			node, _ := g.GetNode(item.ID)
			// Sync text changes to disk.
			if item.Text != nil && node.File != "" {
				filePath := strings.TrimPrefix(node.File, "file:")
				var diskErr error
				switch node.Kind {
				case graph.KindFunction:
					diskErr = edit.FunctionBody(filePath, node.Name, node.Metadata["receiver"], *item.Text)
				case graph.KindType:
					diskErr = edit.TypeBody(filePath, node.Name, *item.Text)
				default:
					if isStmtNode(node.Kind) {
						// Statement updated: find enclosing function and regenerate its body.
						if funcID, fp, funcName, receiver, ok := findEnclosingFunc(g, item.ID); ok {
							if body, ok := codegen.RenderBody(g, funcID); ok {
								diskErr = edit.FunctionBody(fp, funcName, receiver, body)
							}
						}
					}
				}
				if diskErr != nil {
					errs = append(errs, fmt.Sprintf("%s (disk): %v", item.ID, diskErr))
				}
			} else if item.Metadata != nil && node.File != "" && isStmtNode(node.Kind) {
				// Metadata-only update on statement node: also regenerate body.
				if funcID, fp, funcName, receiver, ok := findEnclosingFunc(g, item.ID); ok {
					if body, ok := codegen.RenderBody(g, funcID); ok {
						if diskErr := edit.FunctionBody(fp, funcName, receiver, body); diskErr != nil {
							errs = append(errs, fmt.Sprintf("%s (disk): %v", item.ID, diskErr))
						}
					}
				}
			}
			lines = append(lines, fmt.Sprintf("s %s %s", node.ID, node.Name))
		}
		out := fmt.Sprintf("updated %d/%d nodes", len(lines), len(input.Nodes))
		if len(errs) > 0 {
			out += "; errors: " + strings.Join(errs, "; ")
		}
		if len(lines) > 0 {
			out += "\n" + strings.Join(lines, "\n")
		}
		return toolText(out), nil, nil
	})

	type addEdgeInput struct {
		From string `json:"from" jsonschema:"source node ID"`
		To   string `json:"to" jsonschema:"target node ID"`
		Kind string `json:"kind" jsonschema:"edge kind: contains, calls, references, annotates, covers"`
	}

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
				errs = append(errs, fmt.Sprintf("%s\u2192%s: %v", item.From, item.To, err))
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
		Name:        "delete_edges",
		Description: "Remove multiple edges in a single call. Returns count removed and any errors.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input addEdgesInput) (*mcp.CallToolResult, any, error) {
		var errs []string
		removed := 0
		for _, item := range input.Edges {
			if err := g.RemoveEdge(item.From, item.To, graph.EdgeKind(item.Kind)); err != nil {
				errs = append(errs, fmt.Sprintf("%s\u2192%s: %v", item.From, item.To, err))
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

	type renameInput struct {
		ID      string `json:"id" jsonschema:"node ID of the symbol to rename, e.g. func:ParseFile, type:Config, var:errNotFound"`
		NewName string `json:"new_name" jsonschema:"new symbol name (not the full ID \u2014 just the name part, e.g. ParseGoFile)"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "rename_symbol",
		Description: "Rename a symbol (function, type, variable, or local variable) across the entire codebase. " +
			"Updates the symbol definition, all callers/references in function bodies, " +
			"and the graph node ID and edges atomically. " +
			"Supports: functions, methods, types, variables, constants, local variables (KindLocal).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input renameInput) (*mcp.CallToolResult, any, error) {
		if input.ID == "" || input.NewName == "" {
			return toolError("id and new_name are required"), nil, nil
		}

		node, ok := g.GetNode(input.ID)
		if !ok {
			return toolError(fmt.Sprintf("node %q not found", input.ID)), nil, nil
		}
		oldName := node.Name

		if oldName == input.NewName {
			return toolText("no change \u2014 name is already " + input.NewName), nil, nil
		}

		// Special handling for KindLocal (variable dictionary nodes).
		// Update all referencing statement metadata and regenerate function bodies.
		if node.Kind == graph.KindLocal {
			// Collect all statement nodes that reference this local variable.
			affectedFuncs := make(map[string]bool) // funcID → true
			for _, e := range g.EdgesTo(input.ID) {
				if e.Kind != graph.EdgeReferences && e.Kind != graph.EdgeContains {
					continue
				}
				stmtNode, exists := g.GetNode(e.From)
				if !exists || !isStmtNode(stmtNode.Kind) {
					continue
				}
				// Replace old name in all statement metadata fields.
				patch := graph.NodePatch{Metadata: map[string]string{}}
				for _, key := range []string{"src", "lhs", "rhs", "cond", "over", "call", "val", "values"} {
					if v, ok := stmtNode.Metadata[key]; ok && strings.Contains(v, oldName) {
						patch.Metadata[key] = replaceWord(v, oldName, input.NewName)
					}
				}
				if len(patch.Metadata) > 0 {
					_ = g.UpdateNode(stmtNode.ID, patch)
				}
				// Track the enclosing function for body regeneration.
				if funcID, _, _, _, ok := findEnclosingFunc(g, stmtNode.ID); ok {
					affectedFuncs[funcID] = true
				}
			}

			// Regenerate each affected function body on disk.
			var errs []string
			for funcID := range affectedFuncs {
				if _, fp, funcName, receiver, ok := findEnclosingFunc(g, funcID); ok {
					if body, hasBody := codegen.RenderBody(g, funcID); hasBody {
						if err := edit.FunctionBody(fp, funcName, receiver, body); err != nil {
							errs = append(errs, fmt.Sprintf("%s: %v", funcID, err))
						}
					}
				}
			}

			// Update the variable node's Name and ID.
			newID := strings.Replace(input.ID, oldName, input.NewName, 1)
			if err := g.RenameNode(input.ID, newID, input.NewName); err != nil {
				errs = append(errs, fmt.Sprintf("graph rename: %v", err))
			}

			msg := fmt.Sprintf("renamed local %s \u2192 %s: updated %d function(s)", oldName, input.NewName, len(affectedFuncs))
			if len(errs) > 0 {
				msg += "; errors: " + strings.Join(errs, "; ")
			}
			return toolText(msg), nil, nil
		}

		// Compute the new node ID by replacing the name component.
		newID := strings.Replace(input.ID, oldName, input.NewName, 1)

		// Find all files that need updating:
		// - the file containing the symbol definition
		// - files containing functions that call/reference this symbol
		filesToUpdate := make(map[string]bool)
		if node.File != "" {
			filesToUpdate[strings.TrimPrefix(node.File, "file:")] = true
		}

		// Walk callee edges pointing TO this node.
		for _, e := range g.EdgesTo(input.ID) {
			if e.Kind != graph.EdgeCallee && e.Kind != graph.EdgeReferences {
				continue
			}
			// The source is a call node; find its parent function.
			for _, pe := range g.EdgesTo(e.From) {
				if pe.Kind == graph.EdgeContains {
					caller, ok := g.GetNode(pe.From)
					if ok && caller.Kind == graph.KindFunction && caller.File != "" {
						filesToUpdate[strings.TrimPrefix(caller.File, "file:")] = true
					}
				}
			}
		}

		// Also check functions whose params/returns reference this type by name.
		if node.Kind == graph.KindType {
			for _, n := range g.NodesByKind(graph.KindFunction) {
				if strings.Contains(n.Metadata["params"], oldName) ||
					strings.Contains(n.Metadata["returns"], oldName) {
					if n.File != "" {
						filesToUpdate[strings.TrimPrefix(n.File, "file:")] = true
					}
				}
			}
		}

		// Rename identifier in every affected file.
		// Use precise (type-based) rename if qualified_id is available,
		// otherwise fall back to name-based rename.
		qualifiedID := node.Metadata["qualified_id"]
		pkgDir := node.Metadata["pkg_path"] // import path, not dir \u2014 use file dir
		if node.File != "" {
			pkgDir = filepath.Dir(strings.TrimPrefix(node.File, "file:"))
		}

		type fileResult struct {
			path  string
			count int
			err   error
		}
		var results []fileResult
		for path := range filesToUpdate {
			var count int
			var err error
			if qualifiedID != "" {
				count, err = edit.RenameIdentifierPrecise(path, pkgDir, qualifiedID, oldName, input.NewName)
			} else {
				count, err = edit.RenameIdentifier(path, oldName, input.NewName)
			}
			results = append(results, fileResult{path, count, err})
		}

		// Update graph: rename node ID and update edges.
		if err := g.RenameNode(input.ID, newID, input.NewName); err != nil {
			return toolError(fmt.Sprintf("graph rename: %v", err)), nil, nil
		}

		// Also update File field metadata that stored the old name in params/returns.
		if node.Kind == graph.KindFunction {
			for _, fn := range g.NodesByKind(graph.KindFunction) {
				patch := graph.NodePatch{}
				changed := false
				if strings.Contains(fn.Metadata["params"], oldName) {
					newParams := strings.ReplaceAll(fn.Metadata["params"], oldName, input.NewName)
					patch.Metadata = map[string]string{"params": newParams}
					changed = true
				}
				if strings.Contains(fn.Metadata["returns"], oldName) {
					newRet := strings.ReplaceAll(fn.Metadata["returns"], oldName, input.NewName)
					if patch.Metadata == nil {
						patch.Metadata = map[string]string{}
					}
					patch.Metadata["returns"] = newRet
					changed = true
				}
				if changed {
					_ = g.UpdateNode(fn.ID, patch)
				}
			}
		}

		// Build summary.
		totalReplacements := 0
		var errs []string
		var fileSummary []string
		for _, r := range results {
			if r.err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", r.path, r.err))
			} else {
				totalReplacements += r.count
				fileSummary = append(fileSummary, fmt.Sprintf("%s (%d)", r.path, r.count))
			}
		}

		msg := fmt.Sprintf("renamed %s \u2192 %s: %d replacement(s) across %d file(s)",
			oldName, input.NewName, totalReplacements, len(filesToUpdate))
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
	// Include body text for content-bearing nodes (functions, types, variables, comments).
	// Statement nodes (for, if, switch, etc.) use metadata instead.
	switch n.Kind {
	case graph.KindFunction, graph.KindType, graph.KindVariable, graph.KindComment:
		if n.Text != "" {
			escaped := strings.ReplaceAll(n.Text, "\n", "\\n")
			fmt.Fprintf(&b, "a %s text %s\n", id, escaped)
		}
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
			r := toolNode(e.n)
			b.WriteString(r.Content[0].(*mcp.TextContent).Text)
			fmt.Fprintf(&b, "\na %s heap_allocs %d\n\n", e.n.ID, e.heaps)
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

func registerDeleteTools(srv *mcp.Server, g *graph.Graph) {
	type deleteNodesInput struct {
		IDs []string `json:"ids" jsonschema:"array of node IDs to delete"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "delete_nodes",
		Description: "Delete nodes from the graph and remove all their edges. " +
			"For function nodes, removes the function declaration from its source file. " +
			"For type nodes, removes the type declaration from its source file. " +
			"For statement nodes, regenerates the enclosing function body and patches the file. " +
			"Returns count deleted and any errors.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input deleteNodesInput) (*mcp.CallToolResult, any, error) {
		var errs []string
		deleted := 0
		for _, id := range input.IDs {
			node, ok := g.GetNode(id)
			if !ok {
				errs = append(errs, fmt.Sprintf("%s: not found", id))
				continue
			}
			switch node.Kind {
			case graph.KindFunction:
				if node.File != "" {
					filePath := strings.TrimPrefix(node.File, "file:")
					if err := edit.DeleteFunction(filePath, node.Name, node.Metadata["receiver"]); err != nil {
						errs = append(errs, fmt.Sprintf("%s (disk): %v", id, err))
					}
				}
			case graph.KindType:
				if node.File != "" {
					filePath := strings.TrimPrefix(node.File, "file:")
					if err := edit.DeleteType(filePath, node.Name); err != nil {
						errs = append(errs, fmt.Sprintf("%s (disk): %v", id, err))
					}
				}
			default:
				if isStmtNode(node.Kind) {
					// For statement nodes, find enclosing function and regenerate body.
					if funcID, filePath, funcName, receiver, ok := findEnclosingFunc(g, id); ok {
						// Remove the node from graph first so RenderBody excludes it.
						if err := g.DeleteNode(id); err != nil {
							errs = append(errs, fmt.Sprintf("%s: %v", id, err))
							continue
						}
						deleted++
						if body, ok := codegen.RenderBody(g, funcID); ok {
							if err := edit.FunctionBody(filePath, funcName, receiver, body); err != nil {
								errs = append(errs, fmt.Sprintf("%s (disk): %v", id, err))
							}
						}
						continue
					}
				}
			}
			if err := g.DeleteNode(id); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", id, err))
				continue
			}
			deleted++
		}
		msg := fmt.Sprintf("deleted %d/%d nodes", deleted, len(input.IDs))
		if len(errs) > 0 {
			msg += "; errors: " + strings.Join(errs, "; ")
		}
		return toolText(msg), nil, nil
	})
}

func isStmtNode(kind graph.NodeKind) bool {
	switch kind {
	case graph.KindFor, graph.KindIf, graph.KindSwitch, graph.KindSelect,
		graph.KindReturn, graph.KindDefer, graph.KindGo, graph.KindAssign,
		graph.KindSend, graph.KindBranch, graph.KindStatement,
		graph.KindForRange, graph.KindForCond, graph.KindForLoop, graph.KindForBare,
		graph.KindAssignDecl, graph.KindAssignSet, graph.KindAssignOp,
		graph.KindAssignInc, graph.KindAssignDec,
		graph.KindBranchBreak, graph.KindBranchContinue,
		graph.KindBranchGoto, graph.KindBranchFall:
		return true
	}
	return false
}

func findEnclosingFunc(g *graph.Graph, nodeID string) (funcID, filePath, funcName, receiver string, ok bool) {
	// Walk EdgesTo with EdgeContains upward until KindFunction.
	current := nodeID
	for i := 0; i < 32; i++ {
		for _, e := range g.EdgesTo(current) {
			if e.Kind != graph.EdgeContains {
				continue
			}
			parent, exists := g.GetNode(e.From)
			if !exists {
				continue
			}
			if parent.Kind == graph.KindFunction {
				filePath := strings.TrimPrefix(parent.File, "file:")
				return parent.ID, filePath, parent.Name, parent.Metadata["receiver"], true
			}
			current = parent.ID
			break
		}
		// No EdgeContains parent found.
		break
	}
	return "", "", "", "", false
}

func registerImportTools(srv *mcp.Server, g *graph.Graph) {
	type addImportInput struct {
		FileID     string `json:"file_id" jsonschema:"file node ID, e.g. file:/path/to/foo.go"`
		ImportPath string `json:"import_path" jsonschema:"import path to add, e.g. \"fmt\" or \"github.com/foo/bar\""`
		Alias      string `json:"alias,omitempty" jsonschema:"optional import alias, e.g. myfmt"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_import",
		Description: "Add an import to a Go source file and update the graph's import nodes. No-op if the import already exists.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input addImportInput) (*mcp.CallToolResult, any, error) {
		if input.FileID == "" || input.ImportPath == "" {
			return toolError("file_id and import_path are required"), nil, nil
		}
		filePath := strings.TrimPrefix(input.FileID, "file:")
		if err := edit.AddImport(filePath, input.ImportPath, input.Alias); err != nil {
			return toolError(err.Error()), nil, nil
		}
		// Update graph: add import node if not present.
		importID := fmt.Sprintf("import:%s:%s", input.FileID, input.ImportPath)
		meta := map[string]string{}
		if input.Alias != "" {
			meta["alias"] = input.Alias
		}
		_ = g.AddNode(&graph.Node{
			ID:       importID,
			Kind:     graph.KindImport,
			Name:     input.ImportPath,
			File:     input.FileID,
			Metadata: meta,
		})
		_ = g.AddEdge(input.FileID, importID, graph.EdgeContains)
		return toolText(fmt.Sprintf("added import %q to %s", input.ImportPath, input.FileID)), nil, nil
	})

	type removeImportInput struct {
		FileID     string `json:"file_id" jsonschema:"file node ID, e.g. file:/path/to/foo.go"`
		ImportPath string `json:"import_path" jsonschema:"import path to remove, e.g. \"fmt\""`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "remove_import",
		Description: "Remove an import from a Go source file and remove the corresponding import node from the graph.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input removeImportInput) (*mcp.CallToolResult, any, error) {
		if input.FileID == "" || input.ImportPath == "" {
			return toolError("file_id and import_path are required"), nil, nil
		}
		filePath := strings.TrimPrefix(input.FileID, "file:")
		if err := edit.RemoveImport(filePath, input.ImportPath); err != nil {
			return toolError(err.Error()), nil, nil
		}
		// Remove import node from graph.
		importID := fmt.Sprintf("import:%s:%s", input.FileID, input.ImportPath)
		_ = g.DeleteNode(importID)
		return toolText(fmt.Sprintf("removed import %q from %s", input.ImportPath, input.FileID)), nil, nil
	})
}

func replaceWord(s, oldWord, newWord string) string {
	// replaceWord replaces oldWord with newWord only when oldWord appears as a
	// complete identifier (surrounded by non-identifier chars or at boundaries).
	if oldWord == "" {
		return s
	}
	re := regexp.MustCompile(`(?m)\b` + regexp.QuoteMeta(oldWord) + `\b`)
	return re.ReplaceAllString(s, newWord)
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

## Mutate (all sync to disk immediately)

  add_nodes(nodes[])         — add nodes; kind=file creates file, kind=function appends
  update_nodes(nodes[])      — update nodes; function/type/statement bodies patch file
  add_edges(edges[])         — add edges
  delete_edges(edges[])      — remove edges
  delete_nodes(ids[])        — remove nodes; function/type deletes from file, statement regenerates body

## Structural editing

  add_import(file_id, import_path, alias?)  — add import to file + graph
  remove_import(file_id, import_path)       — remove import from file + graph
  rename_symbol(id, new_name)              — rename across codebase (uses go/types when available)
  find_deadcode(include_exported?)          — unreferenced symbols

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

## Refactor

  move_function(id, target_file_id)       — move function to another file
  extract_interface(type_id, interface_name, target_file_id?)  — generate interface from concrete type's exported methods
  find_implementations(interface_id)      — find types that implement an interface

## Graph snapshots

  graph_snapshot(name?)                   — snapshot current node set to memory
  graph_diff(snapshot_name)              — diff current graph vs snapshot: added/deleted/changed

## Specs

  create_spec(id, kind, namespace?, title, body?)
  get_spec(id)
  list_specs(kind?, namespace?)
  link_spec(spec_id, node_id)
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
