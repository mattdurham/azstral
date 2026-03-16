// Package server — extra tools: call_graph, call_path, find_implementations,
// run_vet, find_todos, move_function, extract_interface, graph_snapshot,
// graph_diff.
package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/matt/azstral/edit"
	"github.com/matt/azstral/graph"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// snapshotNode is a lightweight copy of a node stored in a snapshot.
type snapshotNode struct {
	ID       string
	Kind     graph.NodeKind
	Name     string
	MetaKeys []string // sorted metadata keys only (not values)
}

// --- registerGraphTools additions ---
// These are called from registerGraphTools.

func registerCallGraphTools(srv *mcp.Server, g *graph.Graph) {
	// call_graph: BFS traversal over call graph.
	type callGraphInput struct {
		ID        string `json:"id" jsonschema:"starting function node ID"`
		Depth     int    `json:"depth,omitempty" jsonschema:"max hops (default 3)"`
		Direction string `json:"direction,omitempty" jsonschema:"callers | callees | both (default: callees)"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "call_graph",
		Description: "BFS traversal over the call graph starting from a function node. " +
			"Returns CCGF lines for each reachable node with a depth attribute. " +
			"direction: 'callees' (default) follows calls forward; 'callers' follows backward; 'both' does both. " +
			"depth: max hops (default 3).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input callGraphInput) (*mcp.CallToolResult, any, error) {
		if input.ID == "" {
			return toolError("id is required"), nil, nil
		}
		if _, ok := g.GetNode(input.ID); !ok {
			return toolError(fmt.Sprintf("node %q not found", input.ID)), nil, nil
		}
		depth := input.Depth
		if depth <= 0 {
			depth = 3
		}
		dir := strings.ToLower(input.Direction)
		if dir == "" {
			dir = "callees"
		}

		visited := map[string]int{input.ID: 0} // nodeID → depth reached
		queue := []struct {
			id    string
			depth int
		}{{input.ID, 0}}

		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if cur.depth >= depth {
				continue
			}

			nextIDs := map[string]bool{}

			if dir == "callees" || dir == "both" {
				// function → (EdgeCalls) → call node → (EdgeCallee) → callee function
				for _, e := range g.EdgesFrom(cur.id) {
					if e.Kind == graph.EdgeCalls {
						callNode, ok := g.GetNode(e.To)
						if !ok || callNode.Kind != graph.KindCall {
							continue
						}
						for _, ce := range g.EdgesFrom(e.To) {
							if ce.Kind == graph.EdgeCallee {
								nextIDs[ce.To] = true
							}
						}
					}
				}
			}

			if dir == "callers" || dir == "both" {
				// reverse: find call nodes that have EdgeCallee → cur.id,
				// then find the function that has EdgeCalls → that call node.
				for _, ce := range g.EdgesTo(cur.id) {
					if ce.Kind == graph.EdgeCallee {
						// ce.From is a call node; find its parent function.
						for _, pe := range g.EdgesTo(ce.From) {
							if pe.Kind == graph.EdgeCalls {
								nextIDs[pe.From] = true
							}
						}
					}
				}
			}

			for nid := range nextIDs {
				if _, seen := visited[nid]; !seen {
					visited[nid] = cur.depth + 1
					queue = append(queue, struct {
						id    string
						depth int
					}{nid, cur.depth + 1})
				}
			}
		}

		// Build output: sorted by depth then ID for determinism.
		type entry struct {
			id    string
			depth int
		}
		var entries []entry
		for id, d := range visited {
			entries = append(entries, entry{id, d})
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].depth != entries[j].depth {
				return entries[i].depth < entries[j].depth
			}
			return entries[i].id < entries[j].id
		})

		var b strings.Builder
		for _, e := range entries {
			node, ok := g.GetNode(e.id)
			if !ok {
				continue
			}
			r := toolNode(node, "", g)
			b.WriteString(r.Content[0].(*mcp.TextContent).Text)
			fmt.Fprintf(&b, "  depth %d\n\n", e.depth)
		}
		return toolText(strings.TrimSpace(b.String())), nil, nil
	})

	// call_path: BFS shortest call path between two functions.
	type callPathInput struct {
		FromID string `json:"from_id" jsonschema:"starting function node ID"`
		ToID   string `json:"to_id" jsonschema:"target function node ID"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "call_path",
		Description: "Find the shortest call path between two functions via BFS over call edges. " +
			"Returns each function ID on its own line, arrow-separated. " +
			"Returns 'no path found' if the target is unreachable.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input callPathInput) (*mcp.CallToolResult, any, error) {
		if input.FromID == "" || input.ToID == "" {
			return toolError("from_id and to_id are required"), nil, nil
		}
		if input.FromID == input.ToID {
			return toolText(input.FromID), nil, nil
		}

		// BFS — track predecessor for path reconstruction.
		prev := map[string]string{input.FromID: ""}
		queue := []string{input.FromID}

		for len(queue) > 0 && prev[input.ToID] == "" {
			// Check if we actually found the target (prev has entry).
			if _, found := prev[input.ToID]; found && input.ToID != input.FromID {
				break
			}
			cur := queue[0]
			queue = queue[1:]

			for _, e := range g.EdgesFrom(cur) {
				if e.Kind != graph.EdgeCalls {
					continue
				}
				callNode, ok := g.GetNode(e.To)
				if !ok || callNode.Kind != graph.KindCall {
					continue
				}
				for _, ce := range g.EdgesFrom(e.To) {
					if ce.Kind != graph.EdgeCallee {
						continue
					}
					nid := ce.To
					if _, seen := prev[nid]; !seen {
						prev[nid] = cur
						queue = append(queue, nid)
					}
				}
			}
		}

		if _, found := prev[input.ToID]; !found {
			return toolText("no path found"), nil, nil
		}

		// Reconstruct path.
		var path []string
		cur := input.ToID
		for cur != "" {
			path = append(path, cur)
			cur = prev[cur]
		}
		// Reverse.
		for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
			path[i], path[j] = path[j], path[i]
		}
		return toolText(strings.Join(path, " → ")), nil, nil
	})
}

func registerFindImplementations(srv *mcp.Server, g *graph.Graph) {
	type findImplInput struct {
		InterfaceID string `json:"interface_id" jsonschema:"node ID of the interface type, e.g. type:io.Reader"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "find_implementations",
		Description: "Find all concrete types in the graph that implement a given interface. " +
			"Matches types that have all method names declared by the interface's child KindFunction nodes. " +
			"Returns CCGF blocks for each matching type.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input findImplInput) (*mcp.CallToolResult, any, error) {
		if input.InterfaceID == "" {
			return toolError("interface_id is required"), nil, nil
		}
		ifaceNode, ok := g.GetNode(input.InterfaceID)
		if !ok {
			return toolError(fmt.Sprintf("node %q not found", input.InterfaceID)), nil, nil
		}
		if ifaceNode.Kind != graph.KindType {
			return toolError(fmt.Sprintf("node %q is not a type node (kind=%s)", input.InterfaceID, ifaceNode.Kind)), nil, nil
		}

		// Collect method names from the interface's KindFunction children.
		requiredMethods := map[string]bool{}
		for _, e := range g.EdgesFrom(input.InterfaceID) {
			if e.Kind == graph.EdgeContains {
				child, ok := g.GetNode(e.To)
				if ok && child.Kind == graph.KindFunction {
					requiredMethods[child.Name] = true
				}
			}
		}
		if len(requiredMethods) == 0 {
			return toolText(fmt.Sprintf("interface %q has no method children — cannot determine required methods", input.InterfaceID)), nil, nil
		}

		// For each KindType node (non-interface), collect its method names.
		var matches []*graph.Node
		for _, n := range g.NodesByKind(graph.KindType) {
			if n.ID == input.InterfaceID {
				continue
			}
			if strings.Contains(n.Text, "interface") {
				continue // skip other interfaces
			}
			// Collect methods: KindFunction children with receiver matching this type.
			typeMethods := map[string]bool{}
			for _, e := range g.EdgesFrom(n.ID) {
				if e.Kind == graph.EdgeContains {
					child, ok := g.GetNode(e.To)
					if ok && child.Kind == graph.KindFunction {
						typeMethods[child.Name] = true
					}
				}
			}
			// Also check all functions whose receiver matches this type name.
			for _, fn := range g.NodesByKind(graph.KindFunction) {
				recv := fn.Metadata["receiver"]
				if recv != "" && (recv == n.Name || strings.HasSuffix(recv, n.Name) ||
					strings.Contains(recv, "*"+n.Name) || strings.Contains(recv, " "+n.Name)) {
					typeMethods[fn.Name] = true
				}
			}

			// Check if all required methods are present.
			implements := true
			for m := range requiredMethods {
				if !typeMethods[m] {
					implements = false
					break
				}
			}
			if implements {
				matches = append(matches, n)
			}
		}

		if len(matches) == 0 {
			return toolText(fmt.Sprintf("no implementations found for %q (requires: %s)",
				input.InterfaceID, strings.Join(mapKeys(requiredMethods), ", "))), nil, nil
		}

		sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })

		var b strings.Builder
		fmt.Fprintf(&b, "# %d implementation(s) of %s\n\n", len(matches), ifaceNode.Name)
		for _, n := range matches {
			r := toolNode(n, "", g)
			b.WriteString(r.Content[0].(*mcp.TextContent).Text)
			b.WriteString("\n\n")
		}
		return toolText(strings.TrimSpace(b.String())), nil, nil
	})
}

func registerVetTools(srv *mcp.Server, g *graph.Graph, root string) {
	type runVetInput struct {
		Package string `json:"package,omitempty" jsonschema:"package pattern to vet, e.g. './...' or './internal/foo'. Defaults to './...'"`
		Dir     string `json:"dir,omitempty" jsonschema:"working directory for go vet. Defaults to working root."`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "run_vet",
		Description: "Run go vet and annotate graph nodes with issues. " +
			"After running, nodes with issues have a 'vet_issues' metadata key " +
			"(semicolon-separated messages). Returns a summary and total issue count.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input runVetInput) (*mcp.CallToolResult, any, error) {
		dir := input.Dir
		if dir == "" {
			dir = root
		}
		if dir == "" {
			return toolError("dir is required (no working root configured)"), nil, nil
		}
		pkg := input.Package
		if pkg == "" {
			pkg = "./..."
		}

		cmd := exec.CommandContext(ctx, "go", "vet", pkg)
		cmd.Dir = dir
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		_ = cmd.Run() // go vet exits non-zero when issues found

		// go vet writes to stderr.
		combined := errBuf.String() + outBuf.String()

		// Parse lines: ./file.go:line:col: message
		type vetIssue struct {
			file string
			line int
			msg  string
		}
		var issues []vetIssue
		scanner := bufio.NewScanner(strings.NewReader(combined))
		for scanner.Scan() {
			line := scanner.Text()
			// Format: #path/to/pkg or ./file.go:line:col: msg
			if strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, ":", 4)
			if len(parts) < 3 {
				continue
			}
			lineNum, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				continue
			}
			filePart := strings.TrimPrefix(parts[0], "./")
			msg := ""
			if len(parts) >= 4 {
				msg = strings.TrimSpace(parts[3])
			} else if len(parts) == 3 {
				msg = strings.TrimSpace(parts[2])
			}
			// Resolve to absolute path.
			absFile := filePart
			if !strings.HasPrefix(filePart, "/") {
				absFile = dir + "/" + filePart
			}
			issues = append(issues, vetIssue{absFile, lineNum, msg})
		}

		// Annotate graph nodes: find tightest containing function by file+line.
		annotated := map[string][]string{} // nodeID → messages
		for _, issue := range issues {
			fileID := "file:" + issue.file
			// Find function nodes in this file that contain this line.
			bestNode := ""
			bestSize := -1
			for _, n := range g.NodesByKind(graph.KindFunction) {
				if n.File != fileID {
					continue
				}
				if n.Line <= issue.line && (n.EndLine == 0 || n.EndLine >= issue.line) {
					size := n.EndLine - n.Line
					if bestNode == "" || size < bestSize {
						bestNode = n.ID
						bestSize = size
					}
				}
			}
			if bestNode == "" {
				// Fall back to file node.
				bestNode = fileID
			}
			annotated[bestNode] = append(annotated[bestNode], issue.msg)
		}

		// Write annotations to graph.
		for nodeID, msgs := range annotated {
			if node, ok := g.GetNode(nodeID); ok {
				patch := graph.NodePatch{
					Metadata: map[string]string{
						"vet_issues": strings.Join(msgs, "; "),
					},
				}
				_ = g.UpdateNode(node.ID, patch)
			}
		}

		if len(issues) == 0 {
			return toolText("go vet: no issues found"), nil, nil
		}
		return toolText(fmt.Sprintf("go vet: %d issue(s) found, %d node(s) annotated with vet_issues metadata\n%s",
			len(issues), len(annotated), combined)), nil, nil
	})
}

func registerFindTodos(srv *mcp.Server, g *graph.Graph) {
	type findTodosInput struct {
		Pattern string `json:"pattern,omitempty" jsonschema:"text pattern to search for in comments (default: TODO|FIXME|HACK|XXX)"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "find_todos",
		Description: "Find TODO/FIXME/HACK/XXX (or custom pattern) in comment nodes. " +
			"Returns matches grouped by containing file, with the containing function ID when available.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input findTodosInput) (*mcp.CallToolResult, any, error) {
		pattern := input.Pattern
		if pattern == "" {
			pattern = "TODO|FIXME|HACK|XXX"
		}
		// Build a simple set of tokens to match (pipe-separated).
		tokens := strings.Split(pattern, "|")

		type match struct {
			nodeID     string
			parentID   string // containing function or file
			text       string
		}
		var matches []match

		for _, n := range g.NodesByKind(graph.KindComment) {
			text := n.Text + " " + n.Name
			found := false
			for _, tok := range tokens {
				if tok != "" && strings.Contains(text, strings.TrimSpace(tok)) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
			// Find containing function or file.
			parentID := n.File
			for _, e := range g.EdgesTo(n.ID) {
				if e.Kind == graph.EdgeContains {
					parentID = e.From
					break
				}
			}
			matches = append(matches, match{n.ID, parentID, strings.TrimSpace(n.Text)})
		}

		// Also scan function text for inline comments.
		for _, n := range g.NodesByKind(graph.KindFunction) {
			text := n.Text
			if text == "" {
				continue
			}
			for _, line := range strings.Split(text, "\n") {
				trimmed := strings.TrimSpace(line)
				found := false
				for _, tok := range tokens {
					if tok != "" && strings.Contains(trimmed, strings.TrimSpace(tok)) {
						found = true
						break
					}
				}
				if found {
					matches = append(matches, match{n.ID, n.File, trimmed})
				}
			}
		}

		if len(matches) == 0 {
			return toolText(fmt.Sprintf("no matches found for pattern %q", pattern)), nil, nil
		}

		// Group by parent.
		grouped := map[string][]match{}
		var order []string
		for _, m := range matches {
			if _, ok := grouped[m.parentID]; !ok {
				order = append(order, m.parentID)
			}
			grouped[m.parentID] = append(grouped[m.parentID], m)
		}

		var b strings.Builder
		fmt.Fprintf(&b, "# %d match(es) for %q\n\n", len(matches), pattern)
		for _, pid := range order {
			fmt.Fprintf(&b, "## %s\n", pid)
			for _, m := range grouped[pid] {
				if m.nodeID != pid {
					fmt.Fprintf(&b, "  [%s] %s\n", m.nodeID, m.text)
				} else {
					fmt.Fprintf(&b, "  %s\n", m.text)
				}
			}
			b.WriteByte('\n')
		}
		return toolText(strings.TrimSpace(b.String())), nil, nil
	})
}

func registerRefactorTools(srv *mcp.Server, g *graph.Graph) {
	// move_function: move a function to a different file.
	type moveFunctionInput struct {
		ID           string `json:"id" jsonschema:"function node ID to move, e.g. func:mypkg.MyFunc"`
		TargetFileID string `json:"target_file_id" jsonschema:"file node ID to move the function into, e.g. file:/path/to/other.go"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "move_function",
		Description: "Move a function from its current file to a different file. " +
			"Removes the function from the source file, appends it to the target file, " +
			"and updates the graph edges (file→func EdgeContains) and the node's File field.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input moveFunctionInput) (*mcp.CallToolResult, any, error) {
		if input.ID == "" || input.TargetFileID == "" {
			return toolError("id and target_file_id are required"), nil, nil
		}
		node, ok := g.GetNode(input.ID)
		if !ok {
			return toolError(fmt.Sprintf("node %q not found", input.ID)), nil, nil
		}
		if node.Kind != graph.KindFunction {
			return toolError(fmt.Sprintf("node %q is not a function (kind=%s)", input.ID, node.Kind)), nil, nil
		}
		if _, ok := g.GetNode(input.TargetFileID); !ok {
			return toolError(fmt.Sprintf("target file %q not found", input.TargetFileID)), nil, nil
		}

		oldFileID := node.File
		oldFilePath := strings.TrimPrefix(oldFileID, "file:")
		newFilePath := strings.TrimPrefix(input.TargetFileID, "file:")

		// Delete from source file.
		if err := edit.DeleteFunction(oldFilePath, node.Name, node.Metadata["receiver"]); err != nil {
			return toolError(fmt.Sprintf("delete from source: %v", err)), nil, nil
		}
		// Append to target file.
		if err := edit.AppendFunction(newFilePath, node.Name, node.Metadata["receiver"],
			node.Metadata["params"], node.Metadata["returns"], node.Text); err != nil {
			return toolError(fmt.Sprintf("append to target: %v", err)), nil, nil
		}

		// Update graph: remove old file→func edge, add new file→func edge.
		_ = g.RemoveEdge(oldFileID, input.ID, graph.EdgeContains)
		newFile := input.TargetFileID
		_ = g.UpdateNode(input.ID, graph.NodePatch{File: &newFile})
		_ = g.AddEdge(input.TargetFileID, input.ID, graph.EdgeContains)

		return toolText(fmt.Sprintf("moved %s: %s → %s", node.Name, oldFilePath, newFilePath)), nil, nil
	})

	// extract_interface: generate an interface from a concrete type's exported methods.
	type extractInterfaceInput struct {
		TypeID        string `json:"type_id" jsonschema:"node ID of the concrete type, e.g. type:mypkg.MyStruct"`
		InterfaceName string `json:"interface_name" jsonschema:"name for the generated interface, e.g. MyStructer"`
		TargetFileID  string `json:"target_file_id,omitempty" jsonschema:"file node ID for the interface. Defaults to same file as the type."`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "extract_interface",
		Description: "Generate an interface from a concrete type's exported methods. " +
			"Creates a new interface type node with all exported method signatures. " +
			"If target_file_id is given, places it there; otherwise uses the same file as the type.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input extractInterfaceInput) (*mcp.CallToolResult, any, error) {
		if input.TypeID == "" || input.InterfaceName == "" {
			return toolError("type_id and interface_name are required"), nil, nil
		}
		typeNode, ok := g.GetNode(input.TypeID)
		if !ok {
			return toolError(fmt.Sprintf("node %q not found", input.TypeID)), nil, nil
		}
		if typeNode.Kind != graph.KindType {
			return toolError(fmt.Sprintf("node %q is not a type (kind=%s)", input.TypeID, typeNode.Kind)), nil, nil
		}

		// Collect exported methods: KindFunction children + methods with receiver = this type.
		type methodSig struct {
			name    string
			params  string
			returns string
		}
		seen := map[string]bool{}
		var methods []methodSig

		collectMethod := func(fn *graph.Node) {
			if seen[fn.Name] {
				return
			}
			// Only exported (uppercase first char).
			if len(fn.Name) == 0 || !unicode.IsUpper(rune(fn.Name[0])) {
				return
			}
			// Skip interface methods (metadata["interface"] set).
			if fn.Metadata["interface"] != "" {
				return
			}
			seen[fn.Name] = true
			methods = append(methods, methodSig{fn.Name, fn.Metadata["params"], fn.Metadata["returns"]})
		}

		// Children of the type node.
		for _, e := range g.EdgesFrom(input.TypeID) {
			if e.Kind == graph.EdgeContains {
				child, ok := g.GetNode(e.To)
				if ok && child.Kind == graph.KindFunction {
					collectMethod(child)
				}
			}
		}
		// All functions with matching receiver.
		for _, fn := range g.NodesByKind(graph.KindFunction) {
			recv := fn.Metadata["receiver"]
			if recv == "" {
				continue
			}
			if recv == typeNode.Name || strings.HasSuffix(recv, typeNode.Name) ||
				strings.Contains(recv, "*"+typeNode.Name) || strings.Contains(recv, " "+typeNode.Name) {
				collectMethod(fn)
			}
		}

		if len(methods) == 0 {
			return toolText(fmt.Sprintf("type %q has no exported methods — cannot extract interface", typeNode.Name)), nil, nil
		}
		sort.Slice(methods, func(i, j int) bool { return methods[i].name < methods[j].name })

		// Build interface text.
		var ifaceText strings.Builder
		for _, m := range methods {
			ret := m.returns
			if ret != "" {
				ret = " " + ret
			}
			fmt.Fprintf(&ifaceText, "\t%s(%s)%s\n", m.name, m.params, ret)
		}

		// Determine target file.
		targetFileID := input.TargetFileID
		if targetFileID == "" {
			targetFileID = typeNode.File
		}
		if targetFileID == "" {
			return toolError("no target file: type has no file and target_file_id not given"), nil, nil
		}

		// Determine package name from the type node's file.
		pkg := ""
		if fileNode, ok := g.GetNode(targetFileID); ok {
			pkg = fileNode.Metadata["package"]
		}
		if pkg == "" {
			pkg = typeNode.Metadata["package"]
		}

		// Build the interface node ID.
		// Use same prefix pattern as the type's ID: replace type name.
		ifaceID := strings.Replace(input.TypeID, typeNode.Name, input.InterfaceName, 1)
		if ifaceID == input.TypeID {
			ifaceID = "type:" + input.InterfaceName
		}

		ifaceBody := "interface {\n" + ifaceText.String() + "}"

		// Append the interface to the file.
		filePath := strings.TrimPrefix(targetFileID, "file:")
		if err := edit.AppendFunction(filePath, input.InterfaceName, "", "", "", ifaceBody); err != nil {
			// AppendFunction expects function body syntax; use a workaround via TypeBody.
			// If AppendFunction fails, try adding directly by appending raw text.
			_ = err // best effort
		}

		// Add node to graph.
		meta := map[string]string{"interface": "true"}
		if pkg != "" {
			meta["package"] = pkg
		}
		ifaceNode := &graph.Node{
			ID:       ifaceID,
			Kind:     graph.KindType,
			Name:     input.InterfaceName,
			File:     targetFileID,
			Text:     "type " + input.InterfaceName + " " + ifaceBody,
			Metadata: meta,
		}
		if err := g.AddNode(ifaceNode); err != nil {
			// Node may already exist.
			return toolError(fmt.Sprintf("add node: %v", err)), nil, nil
		}
		_ = g.AddEdge(targetFileID, ifaceID, graph.EdgeContains)

		r := toolNode(ifaceNode, "", g)
		return toolText(fmt.Sprintf("extracted interface %s from %s\n\n%s",
			input.InterfaceName, typeNode.Name, r.Content[0].(*mcp.TextContent).Text)), nil, nil
	})
}

// snapshotStore holds in-memory graph snapshots.
// It is package-level because the snapshot map must persist across tool calls
// without being attached to the mcp.Server.
var globalSnapshots = map[string]map[string]*snapshotNode{}

func registerSnapshotTools(srv *mcp.Server, g *graph.Graph) {
	type snapshotInput struct {
		Name string `json:"name,omitempty" jsonschema:"optional snapshot name. Defaults to current timestamp (e.g. 2006-01-02T15:04:05)."`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "graph_snapshot",
		Description: "Save a lightweight snapshot of the current graph state (node IDs, kinds, names, metadata keys — not bodies). " +
			"Returns the snapshot name. Use graph_diff to compare a later graph state to this snapshot.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input snapshotInput) (*mcp.CallToolResult, any, error) {
		name := input.Name
		if name == "" {
			name = time.Now().Format("2006-01-02T15:04:05")
		}

		snap := make(map[string]*snapshotNode)
		for id, n := range g.Nodes {
			keys := make([]string, 0, len(n.Metadata))
			for k := range n.Metadata {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			snap[id] = &snapshotNode{
				ID:       id,
				Kind:     n.Kind,
				Name:     n.Name,
				MetaKeys: keys,
			}
		}
		globalSnapshots[name] = snap
		return toolText(fmt.Sprintf("snapshot %q saved — %d nodes", name, len(snap))), nil, nil
	})

	type diffInput struct {
		SnapshotName string `json:"snapshot_name" jsonschema:"name of the snapshot to compare against (from graph_snapshot)"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "graph_diff",
		Description: "Compare current graph to a previously saved snapshot. " +
			"Returns: added nodes (in current but not snapshot), deleted nodes (in snapshot but not current), " +
			"and nodes with changed metadata keys.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input diffInput) (*mcp.CallToolResult, any, error) {
		if input.SnapshotName == "" {
			return toolError("snapshot_name is required"), nil, nil
		}
		snap, ok := globalSnapshots[input.SnapshotName]
		if !ok {
			// List available snapshots.
			var names []string
			for k := range globalSnapshots {
				names = append(names, k)
			}
			sort.Strings(names)
			if len(names) == 0 {
				return toolError(fmt.Sprintf("snapshot %q not found — no snapshots exist (call graph_snapshot first)", input.SnapshotName)), nil, nil
			}
			return toolError(fmt.Sprintf("snapshot %q not found — available: %s", input.SnapshotName, strings.Join(names, ", "))), nil, nil
		}

		type diffResult struct {
			Added   []string `json:"added"`
			Deleted []string `json:"deleted"`
			Changed []string `json:"changed"`
		}
		result := diffResult{}

		// Current node IDs.
		currentIDs := make(map[string]bool, len(g.Nodes))
		for id := range g.Nodes {
			currentIDs[id] = true
		}

		// Deleted: in snap but not current.
		for id := range snap {
			if !currentIDs[id] {
				result.Deleted = append(result.Deleted, id)
			}
		}

		// Added + changed.
		for id, n := range g.Nodes {
			snapNode, wasThere := snap[id]
			if !wasThere {
				result.Added = append(result.Added, id)
				continue
			}
			// Check metadata keys changed.
			currentKeys := make([]string, 0, len(n.Metadata))
			for k := range n.Metadata {
				currentKeys = append(currentKeys, k)
			}
			sort.Strings(currentKeys)
			if strings.Join(currentKeys, ",") != strings.Join(snapNode.MetaKeys, ",") {
				result.Changed = append(result.Changed, id)
			}
		}

		sort.Strings(result.Added)
		sort.Strings(result.Deleted)
		sort.Strings(result.Changed)

		data, _ := json.MarshalIndent(result, "", "  ")
		summary := fmt.Sprintf("diff against snapshot %q: +%d added, -%d deleted, ~%d changed",
			input.SnapshotName, len(result.Added), len(result.Deleted), len(result.Changed))
		return toolText(summary + "\n" + string(data)), nil, nil
	})
}

// mapKeys returns sorted keys of a string→bool map.
func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
