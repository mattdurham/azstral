// Package ccgf encodes a graph into Compact Code Graph Format.
//
// CCGF is a line-based textual format optimized for LLM consumption.
// It represents program structure as typed nodes and typed edges
// with minimal token overhead.
//
// Format:
//
//	# ccgf1 scope=program vendor=surface
//	s <id> <type> <name> [V]
//	<edge> <from> <to>
//	a <id> <key> <value>
package ccgf

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/matt/azstral/graph"
)

// VendorMode controls how external/vendored dependencies appear.
type VendorMode int

const (
	// VendorSurface includes only the vendor API surface referenced
	// by project code (1 layer deep). This is the default.
	VendorSurface VendorMode = iota
	// VendorInclude includes all vendor nodes fully.
	VendorInclude
)

// Options controls what and how the graph is encoded.
type Options struct {
	// Scope selects what to encode:
	//   ""  or "program" — whole program
	//   "file:<id>"      — single file (e.g. "file:main.go")
	//   "type:<id>"      — single type and its methods
	Scope string

	// Vendor controls external dependency inclusion.
	Vendor VendorMode

	// Attrs enables attribute lines (sig, loc, kind, ro).
	Attrs bool

	// Module is the Go module path, shown in the header.
	Module string
}

// sym is an internal representation of a CCGF symbol.
type sym struct {
	id     string     // short id: s0, s1, ...
	nodeID string     // original graph node ID
	code   string     // CCGF type code: p, f, m, t, i, v, c
	name   string     // qualified name: pkg.Symbol
	vendor bool       // external dependency
	node   *graph.Node
}

// ccgfEdge is an internal edge representation.
type ccgfEdge struct {
	code string // d, c, u, r, m, etc.
	from string // short id
	to   string // short id
}

type encoder struct {
	g      *graph.Graph
	opts   Options
	syms   []*sym
	byNode map[string]*sym   // graph nodeID → sym
	pkgOf  map[string]string // graph nodeID → package name (cached)
	n      int               // id counter
}

// Encode serializes a graph into CCGF text.
func Encode(g *graph.Graph, opts Options) string {
	e := &encoder{
		g:      g,
		opts:   opts,
		byNode: make(map[string]*sym),
		pkgOf:  make(map[string]string),
	}
	e.collect()
	return e.emit()
}

func (e *encoder) nextID() string {
	id := fmt.Sprintf("s%d", e.n)
	e.n++
	return id
}

// packageOf walks contains edges upward to find the package name for a node.
func (e *encoder) packageOf(nodeID string) string {
	if p, ok := e.pkgOf[nodeID]; ok {
		return p
	}
	for _, edge := range e.g.EdgesTo(nodeID) {
		if edge.Kind == graph.EdgeContains {
			parent, ok := e.g.GetNode(edge.From)
			if !ok {
				continue
			}
			if parent.Kind == graph.KindPackage {
				e.pkgOf[nodeID] = parent.Name
				return parent.Name
			}
			// Parent is a file or other container — keep climbing.
			p := e.packageOf(parent.ID)
			if p != "" {
				e.pkgOf[nodeID] = p
				return p
			}
		}
	}
	// External node: extract package from ID like "func:fmt.Println".
	if _, rest, ok := strings.Cut(nodeID, ":"); ok {
		rest = strings.TrimLeft(rest, "*")
		if dot := strings.LastIndex(rest, "."); dot >= 0 {
			pkg := rest[:dot]
			e.pkgOf[nodeID] = pkg
			return pkg
		}
	}
	return ""
}

// typeCode maps a graph node to its CCGF symbol type code.
func typeCode(n *graph.Node) string {
	switch n.Kind {
	case graph.KindPackage:
		return "p"
	case graph.KindFunction:
		if n.Metadata["receiver"] != "" {
			return "m"
		}
		return "f"
	case graph.KindType:
		if strings.Contains(n.Text, "interface") {
			return "i"
		}
		return "t"
	case graph.KindVariable:
		if n.Metadata["const"] == "true" {
			return "c"
		}
		return "v"
	default:
		return ""
	}
}

// qualifiedName builds the canonical CCGF name: pkg.Symbol.
func (e *encoder) qualifiedName(n *graph.Node) string {
	if n.Kind == graph.KindPackage {
		return n.Name
	}
	pkg := e.packageOf(n.ID)
	if pkg != "" {
		return pkg + "." + n.Name
	}
	return n.Name
}

func isVendor(n *graph.Node) bool {
	return n.Metadata["external"] == "true"
}

// importedPackages returns the set of package names that have corresponding
// import nodes in the graph. This is used to filter false vendor detections —
// the parser marks every selector receiver (e.g. "a" in a.Foo()) as an external
// package, but only actual imports are real vendor packages.
func importedPackages(g *graph.Graph) map[string]bool {
	imports := make(map[string]bool)
	for _, n := range g.NodesByKind(graph.KindImport) {
		// Import name is the path (e.g. "fmt", "encoding/json").
		// The package name used in code is the last path component.
		name := n.Name
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		imports[name] = true
		// Also check alias.
		if alias := n.Metadata["alias"]; alias != "" && alias != "." && alias != "_" {
			imports[alias] = true
		}
	}
	return imports
}

// collect gathers nodes, applies scope/vendor filters, and assigns short IDs.
func (e *encoder) collect() {
	// Build the set of actually-imported package names to distinguish real
	// external packages from false positives (local variable selectors).
	imported := importedPackages(e.g)

	// Gather all structural nodes.
	var all []*graph.Node
	for _, kind := range []graph.NodeKind{
		graph.KindPackage,
		graph.KindFunction,
		graph.KindType,
		graph.KindVariable,
	} {
		all = append(all, e.g.NodesByKind(kind)...)
	}

	// Partition into project and vendor, filtering false vendor detections.
	var project, vendor []*graph.Node
	for _, n := range all {
		if !isVendor(n) {
			project = append(project, n)
			continue
		}
		// For external packages: only include if actually imported.
		if n.Kind == graph.KindPackage {
			if imported[n.Name] {
				vendor = append(vendor, n)
			}
			continue
		}
		// For external functions: only include if their package is imported.
		if n.Kind == graph.KindFunction {
			pkg := n.Metadata["package"]
			if pkg == "" {
				pkg = e.packageOf(n.ID)
			}
			if imported[pkg] {
				vendor = append(vendor, n)
			}
			continue
		}
		vendor = append(vendor, n)
	}

	// Apply scope filter to project nodes.
	project = e.filterScope(project)

	// Determine which vendor nodes to include.
	var included []*graph.Node
	included = append(included, project...)

	switch e.opts.Vendor {
	case VendorInclude:
		included = append(included, vendor...)
	case VendorSurface:
		// Only vendor symbols directly referenced by in-scope project code.
		included = append(included, e.vendorSurface(project, vendor)...)
	}

	// Sort: packages first, then non-vendor before vendor, then by name.
	sort.Slice(included, func(i, j int) bool {
		a, b := included[i], included[j]
		aIsPkg := a.Kind == graph.KindPackage
		bIsPkg := b.Kind == graph.KindPackage
		if aIsPkg != bIsPkg {
			return aIsPkg
		}
		aV, bV := isVendor(a), isVendor(b)
		if aV != bV {
			return !aV
		}
		return e.qualifiedName(a) < e.qualifiedName(b)
	})

	// Assign short IDs.
	for _, n := range included {
		tc := typeCode(n)
		if tc == "" {
			continue
		}
		s := &sym{
			id:     e.nextID(),
			nodeID: n.ID,
			code:   tc,
			name:   e.qualifiedName(n),
			vendor: isVendor(n),
			node:   n,
		}
		e.syms = append(e.syms, s)
		e.byNode[n.ID] = s
	}
}

// filterScope restricts project nodes to the requested scope.
func (e *encoder) filterScope(nodes []*graph.Node) []*graph.Node {
	scope := e.opts.Scope
	if scope == "" || scope == "program" {
		return nodes
	}

	if target, ok := strings.CutPrefix(scope, "file:"); ok {
		return e.nodesInFile(nodes, target)
	}
	if target, ok := strings.CutPrefix(scope, "type:"); ok {
		return e.nodesForType(nodes, target)
	}
	return nodes
}

// nodesInFile returns nodes belonging to a single file, plus its parent package.
func (e *encoder) nodesInFile(nodes []*graph.Node, fileID string) []*graph.Node {
	inFile := make(map[string]bool)
	inFile[fileID] = true

	// Direct children of the file.
	for _, c := range e.g.Children(fileID) {
		inFile[c.ID] = true
	}

	// Parent package.
	for _, edge := range e.g.EdgesTo(fileID) {
		if edge.Kind == graph.EdgeContains {
			inFile[edge.From] = true
		}
	}

	var result []*graph.Node
	for _, n := range nodes {
		if inFile[n.ID] {
			result = append(result, n)
		}
	}
	return result
}

// nodesForType returns a type, its methods, and its parent package.
func (e *encoder) nodesForType(nodes []*graph.Node, typeID string) []*graph.Node {
	include := make(map[string]bool)
	include[typeID] = true

	typeNode, ok := e.g.GetNode(typeID)
	if !ok {
		return nil
	}

	// Find parent package.
	pkg := e.packageOf(typeID)
	for _, n := range nodes {
		if n.Kind == graph.KindPackage && n.Name == pkg {
			include[n.ID] = true
		}
	}

	// Find methods with matching receiver type.
	typeName := typeNode.Name
	for _, n := range nodes {
		if n.Kind == graph.KindFunction && n.Metadata["receiver"] != "" {
			recv := n.Metadata["receiver"]
			// Match "x TypeName" or "x *TypeName".
			if strings.Contains(recv, " "+typeName) || strings.Contains(recv, "*"+typeName) {
				include[n.ID] = true
			}
		}
	}

	var result []*graph.Node
	for _, n := range nodes {
		if include[n.ID] {
			result = append(result, n)
		}
	}
	return result
}

// vendorSurface finds external nodes that are directly referenced by project code.
func (e *encoder) vendorSurface(project, vendor []*graph.Node) []*graph.Node {
	seen := make(map[string]bool)
	var result []*graph.Node

	// Index vendor nodes by ID for quick lookup.
	vendorByID := make(map[string]*graph.Node, len(vendor))
	for _, n := range vendor {
		vendorByID[n.ID] = n
	}

	// Walk project functions looking for calls to vendor code.
	for _, n := range project {
		if n.Kind != graph.KindFunction {
			continue
		}
		// Functions contain call nodes.
		for _, edge := range e.g.EdgesFrom(n.ID) {
			if edge.Kind != graph.EdgeContains {
				continue
			}
			callNode, ok := e.g.GetNode(edge.To)
			if !ok || callNode.Kind != graph.KindCall {
				continue
			}
			// Call nodes have callee edges to the target function.
			for _, ce := range e.g.EdgesFrom(callNode.ID) {
				if ce.Kind != graph.EdgeCallee {
					continue
				}
				if _, ok := vendorByID[ce.To]; !ok {
					continue
				}
				if seen[ce.To] {
					continue
				}
				seen[ce.To] = true
				result = append(result, vendorByID[ce.To])

				// Also include the external package.
				pkg := e.packageOf(ce.To)
				for _, vn := range vendor {
					if vn.Kind == graph.KindPackage && vn.Name == pkg && !seen[vn.ID] {
						seen[vn.ID] = true
						result = append(result, vn)
					}
				}
			}
			// Call nodes may also have references edges to vendor packages.
			for _, ce := range e.g.EdgesFrom(callNode.ID) {
				if ce.Kind != graph.EdgeReferences {
					continue
				}
				if vn, ok := vendorByID[ce.To]; ok && !seen[ce.To] {
					seen[ce.To] = true
					result = append(result, vn)
				}
			}
		}
	}

	return result
}

// collectEdges builds CCGF edges from the graph relationships.
func (e *encoder) collectEdges() []ccgfEdge {
	var edges []ccgfEdge
	seen := make(map[string]bool)

	add := func(code, from, to string) {
		key := code + " " + from + " " + to
		if !seen[key] {
			seen[key] = true
			edges = append(edges, ccgfEdge{code, from, to})
		}
	}

	for _, s := range e.syms {
		n := s.node

		switch n.Kind {
		case graph.KindPackage:
			// d (defines): package → symbols it contains (through files).
			for _, fileEdge := range e.g.EdgesFrom(n.ID) {
				if fileEdge.Kind != graph.EdgeContains {
					continue
				}
				for _, childEdge := range e.g.EdgesFrom(fileEdge.To) {
					if childEdge.Kind != graph.EdgeContains {
						continue
					}
					if cs, ok := e.byNode[childEdge.To]; ok {
						add("d", s.id, cs.id)
					}
				}
			}

			// m (imports): package → imported packages.
			for _, fileEdge := range e.g.EdgesFrom(n.ID) {
				if fileEdge.Kind != graph.EdgeContains {
					continue
				}
				for _, childEdge := range e.g.EdgesFrom(fileEdge.To) {
					if childEdge.Kind != graph.EdgeContains {
						continue
					}
					impNode, ok := e.g.GetNode(childEdge.To)
					if !ok || impNode.Kind != graph.KindImport {
						continue
					}
					// Match the imported package name to a symbol.
					importName := impNode.Name
					for _, ts := range e.syms {
						if ts.node.Kind == graph.KindPackage && ts.node.Name == importName {
							add("m", s.id, ts.id)
							break
						}
					}
				}
			}

		case graph.KindFunction:
			// c (calls): function → callee functions/methods.
			for _, edge := range e.g.EdgesFrom(n.ID) {
				if edge.Kind != graph.EdgeContains {
					continue
				}
				callNode, ok := e.g.GetNode(edge.To)
				if !ok || callNode.Kind != graph.KindCall {
					continue
				}
				for _, ce := range e.g.EdgesFrom(callNode.ID) {
					if ce.Kind == graph.EdgeCallee {
						if ts, ok := e.byNode[ce.To]; ok {
							add("c", s.id, ts.id)
						}
					}
				}
			}

			// r (returns): function → return type.
			if ret := n.Metadata["returns"]; ret != "" {
				ret = strings.TrimSpace(ret)
				// Strip pointer, parentheses for matching.
				clean := strings.TrimLeft(ret, "(*")
				clean = strings.TrimRight(clean, ")")
				for _, ts := range e.syms {
					if ts.node.Kind == graph.KindType && ts.node.Name == clean {
						add("r", s.id, ts.id)
					}
				}
			}

			// u (uses): function → types it references in params.
			if params := n.Metadata["params"]; params != "" {
				for _, ts := range e.syms {
					if ts.node.Kind == graph.KindType && strings.Contains(params, ts.node.Name) {
						add("u", s.id, ts.id)
					}
				}
			}
		}
	}

	return edges
}

// emit produces the final CCGF text.
func (e *encoder) emit() string {
	var b strings.Builder

	// Header.
	scope := e.opts.Scope
	if scope == "" {
		scope = "program"
	}
	vendorStr := "surface"
	if e.opts.Vendor == VendorInclude {
		vendorStr = "include"
	}
	fmt.Fprintf(&b, "# ccgf1 scope=%s vendor=%s", scope, vendorStr)
	if e.opts.Module != "" {
		fmt.Fprintf(&b, " mod=%s", e.opts.Module)
	}
	b.WriteByte('\n')

	// Symbol definitions.
	for _, s := range e.syms {
		fmt.Fprintf(&b, "s %s %s %s", s.id, s.code, s.name)
		if s.vendor {
			b.WriteString(" V")
		}
		b.WriteByte('\n')
	}

	// Edges.
	edges := e.collectEdges()
	if len(edges) > 0 {
		b.WriteByte('\n')
		for _, edge := range edges {
			fmt.Fprintf(&b, "%s %s %s\n", edge.code, edge.from, edge.to)
		}
	}

	// Semantic attributes: doc comments and spec IDs (always emitted).
	var semAttrs []string
	for _, s := range e.syms {
		if doc := e.docComment(s.nodeID); doc != "" {
			semAttrs = append(semAttrs, fmt.Sprintf("a %s doc %s", s.id, doc))
		}
		if specs := e.specIDs(s.nodeID); specs != "" {
			semAttrs = append(semAttrs, fmt.Sprintf("a %s specs %s", s.id, specs))
		}
	}
	if len(semAttrs) > 0 {
		b.WriteByte('\n')
		for _, a := range semAttrs {
			b.WriteString(a)
			b.WriteByte('\n')
		}
	}

	// Structural attributes (optional via Attrs flag).
	if e.opts.Attrs {
		var attrs []string
		for _, s := range e.syms {
			n := s.node
			if n.File != "" {
				loc := filepath.Base(n.File)
				if n.Line > 0 {
					loc = fmt.Sprintf("%s:%d", loc, n.Line)
				}
				attrs = append(attrs, fmt.Sprintf("a %s loc %s", s.id, loc))
			}
			if sig := buildSig(n); sig != "" {
				attrs = append(attrs, fmt.Sprintf("a %s sig %s", s.id, sig))
			}
			if n.Kind == graph.KindType {
				kind := "struct"
				if strings.Contains(n.Text, "interface") {
					kind = "interface"
				}
				attrs = append(attrs, fmt.Sprintf("a %s kind %s", s.id, kind))
			}
			if s.vendor {
				attrs = append(attrs, fmt.Sprintf("a %s ro 1", s.id))
			}
		}
		if len(attrs) > 0 {
			b.WriteByte('\n')
			for _, a := range attrs {
				b.WriteString(a)
				b.WriteByte('\n')
			}
		}
	}

	return b.String()
}

// docComment finds the doc comment attached to a node and returns it as a
// single line with // prefixes stripped. Newlines become literal \n.
func (e *encoder) docComment(nodeID string) string {
	var comments []*graph.Node
	for _, edge := range e.g.EdgesTo(nodeID) {
		if edge.Kind != graph.EdgeAnnotates {
			continue
		}
		cn, ok := e.g.GetNode(edge.From)
		if !ok || cn.Kind != graph.KindComment {
			continue
		}
		// Skip trailing file comments.
		if cn.Metadata["trailing"] == "true" {
			continue
		}
		comments = append(comments, cn)
	}
	if len(comments) == 0 {
		return ""
	}
	// Sort by line — multiple comment groups may annotate the same node.
	sort.Slice(comments, func(i, j int) bool {
		return comments[i].Line < comments[j].Line
	})

	var parts []string
	for _, cn := range comments {
		for _, line := range strings.Split(cn.Text, "\n") {
			line = strings.TrimPrefix(line, "// ")
			line = strings.TrimPrefix(line, "//")
			line = strings.TrimSpace(line)
			if line != "" {
				parts = append(parts, line)
			}
		}
	}
	return strings.Join(parts, "\\n")
}

// specIDs finds SPEC/NOTE/TEST/BENCH identifiers that cover a node.
// Returns a comma-separated string like "SPEC-001,TEST-006".
func (e *encoder) specIDs(nodeID string) string {
	seen := make(map[string]bool)
	var ids []string
	for _, edge := range e.g.EdgesTo(nodeID) {
		if edge.Kind != graph.EdgeCovers {
			continue
		}
		sn, ok := e.g.GetNode(edge.From)
		if !ok || sn.Kind != graph.KindSpec {
			continue
		}
		if !seen[sn.Name] {
			seen[sn.Name] = true
			ids = append(ids, sn.Name)
		}
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// buildSig constructs a compact function signature string.
func buildSig(n *graph.Node) string {
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
