package codegen

import (
	"strconv"
	"strings"

	"github.com/matt/azstral/graph"
)

// RenderBody reconstructs a function body from its statement children.
// Returns the body text (without surrounding braces, one-tab de-indented)
// matching the format stored in the function node's Text field.
// Returns ("", false) if the function has no statement children.
func RenderBody(g *graph.Graph, funcID string) (string, bool) {
	children := g.Children(funcID)

	// Filter to statement-level children AND body comments.
	var stmts []*graph.Node
	for _, c := range children {
		if isStmtKind(c.Kind) || isBodyComment(c) {
			stmts = append(stmts, c)
		}
	}
	if len(stmts) == 0 {
		return "", false
	}

	funcNode, _ := g.GetNode(funcID)
	funcLine := 0
	if funcNode != nil {
		funcLine = funcNode.Line
	}

	var b strings.Builder
	renderStmts(g, stmts, &b, "", funcLine)
	return strings.TrimRight(b.String(), "\n"), true
}

// renderStmts emits a list of statements at the given indentation level.
// parentLine is the line of the opening brace of the containing block (for
// blank-line detection after `{`); 0 to skip this check.
func renderStmts(g *graph.Graph, stmts []*graph.Node, b *strings.Builder, indent string, parentLine ...int) {
	prevEndLine := 0
	if len(parentLine) > 0 && parentLine[0] > 0 {
		prevEndLine = parentLine[0]
	}
	for _, s := range stmts {
		// Preserve blank lines between statements (and after opening braces).
		if prevEndLine > 0 && s.Line > prevEndLine+1 {
			b.WriteString("\n")
		}
		renderStmt(g, s, b, indent)
		if s.EndLine > prevEndLine {
			prevEndLine = s.EndLine
		}
	}
}

// renderStmt emits a single statement or body-comment node.
func renderStmt(g *graph.Graph, n *graph.Node, b *strings.Builder, indent string) {
	// Body comments: emit each line with indent.
	if isBodyComment(n) {
		for _, line := range strings.Split(n.Text, "\n") {
			b.WriteString(indent)
			b.WriteString(line)
			b.WriteString("\n")
		}
		return
	}

	src := n.Metadata["src"]
	if src == "" {
		return // no source to reconstruct
	}

	switch n.Kind {
	case graph.KindFor:
		b.WriteString(indent)
		b.WriteString(src)
		b.WriteString(" {\n")
		renderChildren(g, n.ID, b, indent+"\t")
		b.WriteString(indent)
		b.WriteString("}\n")

	case graph.KindSwitch, graph.KindSelect:
		b.WriteString(indent)
		b.WriteString(src)
		b.WriteString(" {\n")
		// Case clauses in switch/select are at the same indent level as the body.
		renderChildren(g, n.ID, b, indent)
		b.WriteString(indent)
		b.WriteString("}\n")

	case graph.KindIf:
		renderIf(g, n, b, indent)

	case graph.KindStatement:
		// case clauses and expression statements
		if n.Name == "case" {
			b.WriteString(indent)
			b.WriteString(src)
			b.WriteString("\n")
			renderChildren(g, n.ID, b, indent+"\t")
		} else {
			b.WriteString(indent)
			b.WriteString(src)
			b.WriteString("\n")
		}

	case graph.KindDefer, graph.KindGo:
		if n.Metadata["closure"] == "true" {
			// Closure: emit header, body children, close suffix.
			b.WriteString(indent)
			b.WriteString(src)
			b.WriteString(" {\n")
			renderChildren(g, n.ID, b, indent+"\t")
			b.WriteString(indent)
			b.WriteString(n.Metadata["close_suffix"])
			b.WriteString("\n")
		} else {
			b.WriteString(indent)
			b.WriteString(src)
			b.WriteString("\n")
		}

	default:
		// Leaf statements: return, assign, send, branch.
		// For multiline src (e.g., return func(y int) int { ... }),
		// only the first line gets the codegen indent — continuation lines
		// have their absolute indentation from the original source.
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			if i == 0 {
				b.WriteString(indent)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
}

// renderIf handles if / else-if / else chains.
func renderIf(g *graph.Graph, n *graph.Node, b *strings.Builder, indent string) {
	src := n.Metadata["src"]
	b.WriteString(indent)
	b.WriteString(src)
	b.WriteString(" {\n")
	renderIfBody(g, n, b, indent)
}

// renderIfBody renders the body + else of an if node at the given indent level.
func renderIfBody(g *graph.Graph, n *graph.Node, b *strings.Builder, indent string) {
	children := g.Children(n.ID)
	var allItems []*graph.Node
	for _, c := range children {
		if isStmtKind(c.Kind) || isBodyComment(c) {
			allItems = append(allItems, c)
		}
	}

	if n.Metadata["has_else"] != "true" {
		renderStmts(g, allItems, b, indent+"\t", n.Line)
		b.WriteString(indent)
		b.WriteString("}\n")
		return
	}

	elseLine := 0
	if el, err := strconv.Atoi(n.Metadata["else_line"]); err == nil {
		elseLine = el
	}

	var ifBody, elseBody []*graph.Node
	var elseIfNode *graph.Node
	for _, c := range allItems {
		if c.Kind == graph.KindIf && c.Line >= elseLine {
			elseIfNode = c
		} else if elseLine > 0 && c.Line > elseLine {
			elseBody = append(elseBody, c)
		} else {
			ifBody = append(ifBody, c)
		}
	}

	renderStmts(g, ifBody, b, indent+"\t", n.Line)

	if elseIfNode != nil {
		// else-if: the "if" keyword is inline after "} else ", body keeps the same indent.
		b.WriteString(indent)
		b.WriteString("} else ")
		b.WriteString(elseIfNode.Metadata["src"])
		b.WriteString(" {\n")
		renderIfBody(g, elseIfNode, b, indent)
	} else if len(elseBody) > 0 {
		b.WriteString(indent)
		b.WriteString("} else {\n")
		renderStmts(g, elseBody, b, indent+"\t", elseLine)
		b.WriteString(indent)
		b.WriteString("}\n")
	} else {
		b.WriteString(indent)
		b.WriteString("} else {\n")
		b.WriteString(indent)
		b.WriteString("}\n")
	}
}

// renderChildren emits all statement children AND body comments of a node,
// interleaved by line number.
func renderChildren(g *graph.Graph, parentID string, b *strings.Builder, indent string) {
	parent, _ := g.GetNode(parentID)
	parentLine := 0
	if parent != nil {
		parentLine = parent.Line
	}
	children := g.Children(parentID)
	var items []*graph.Node
	for _, c := range children {
		if isStmtKind(c.Kind) || isBodyComment(c) {
			items = append(items, c)
		}
	}
	renderStmts(g, items, b, indent, parentLine)
}

// isStmtKind returns true for node kinds that represent statements.
func isStmtKind(k graph.NodeKind) bool {
	switch k {
	case graph.KindFor, graph.KindIf, graph.KindSwitch, graph.KindSelect,
		graph.KindReturn, graph.KindDefer, graph.KindGo, graph.KindAssign,
		graph.KindSend, graph.KindBranch, graph.KindStatement:
		return true
	}
	return false
}

// isBodyComment returns true for comments inside function bodies.
func isBodyComment(n *graph.Node) bool {
	return n.Kind == graph.KindComment && n.Metadata["body_comment"] == "true"
}
