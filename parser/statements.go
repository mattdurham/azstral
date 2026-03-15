package parser

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/matt/azstral/graph"
)

// walkStatements recursively parses Go statements into graph nodes.
// Each statement becomes a child of parentID via EdgeContains.
// Statement IDs are scoped by file path and source position for global uniqueness.
func walkStatements(g *graph.Graph, fset *token.FileSet, src []byte, parentID, fileID string, stmts []ast.Stmt) {
	for _, stmt := range stmts {
		addStmt(g, fset, src, parentID, fileID, stmt)
	}
}

func addStmt(g *graph.Graph, fset *token.FileSet, src []byte, parentID, fileID string, stmt ast.Stmt) {
	if stmt == nil {
		return
	}
	pos := fset.Position(stmt.Pos())
	endPos := fset.Position(stmt.End())
	line, endLine := pos.Line, endPos.Line

	// stmtID encodes kind + file + line for global uniqueness.
	stmtID := func(kind string) string {
		return fmt.Sprintf("%s:%s:%d", kind, fileID, line)
	}

	addNode := func(id string, kind graph.NodeKind, text string, meta map[string]string) {
		if meta == nil {
			meta = map[string]string{}
		}
		_ = g.AddNode(&graph.Node{
			ID:       id,
			Kind:     kind,
			Name:     string(kind),
			File:     fileID,
			Line:     line,
			EndLine:  endLine,
			Text:     text,
			Metadata: meta,
		})
		_ = g.AddEdge(parentID, id, graph.EdgeContains)
	}

	switch s := stmt.(type) {

	case *ast.ForStmt:
		id := stmtID("for")
		cond := ""
		if s.Cond != nil {
			cond = extractText(fset, src, s.Cond.Pos(), s.Cond.End())
		}
		init := ""
		if s.Init != nil {
			init = extractText(fset, src, s.Init.Pos(), s.Init.End())
		}
		post := ""
		if s.Post != nil {
			post = extractText(fset, src, s.Post.Pos(), s.Post.End())
		}
		// src = header for codegen reconstruction: "for init; cond; post"
		header := "for "
		if init != "" || post != "" {
			header += init + "; " + cond + "; " + post
		} else if cond != "" {
			header += cond
		} else {
			header = "for" // bare infinite loop
		}
		addNode(id, graph.KindFor, "", map[string]string{
			"cond": cond, "init": init, "post": post, "src": header,
		})
		if s.Body != nil {
			walkStatements(g, fset, src, id, fileID, s.Body.List)
		}

	case *ast.RangeStmt:
		id := stmtID("for")
		meta := map[string]string{"range": "true"}
		if s.Key != nil {
			meta["key"] = extractText(fset, src, s.Key.Pos(), s.Key.End())
		}
		if s.Value != nil {
			meta["value"] = extractText(fset, src, s.Value.Pos(), s.Value.End())
		}
		over := extractText(fset, src, s.X.Pos(), s.X.End())
		meta["over"] = over
		// src = header for codegen: "for key, value := range over"
		header := "for "
		if meta["key"] != "" && meta["value"] != "" {
			header += meta["key"] + ", " + meta["value"] + " := range " + over
		} else if meta["key"] != "" {
			header += meta["key"] + " := range " + over
		} else {
			header += "range " + over
		}
		meta["src"] = header
		addNode(id, graph.KindFor, "", meta)
		if s.Body != nil {
			walkStatements(g, fset, src, id, fileID, s.Body.List)
		}

	case *ast.IfStmt:
		id := stmtID("if")
		cond := extractText(fset, src, s.Cond.Pos(), s.Cond.End())
		init := ""
		if s.Init != nil {
			init = extractText(fset, src, s.Init.Pos(), s.Init.End())
		}
		header := "if "
		if init != "" {
			header += init + "; " + cond
		} else {
			header += cond
		}
		meta := map[string]string{"cond": cond, "init": init, "src": header}
		if s.Else != nil {
			meta["has_else"] = "true"
			// Store the line where } else { appears so codegen can partition children.
			elsePos := fset.Position(s.Body.Rbrace)
			meta["else_line"] = fmt.Sprintf("%d", elsePos.Line)
		}
		addNode(id, graph.KindIf, "", meta)
		if s.Body != nil {
			walkStatements(g, fset, src, id, fileID, s.Body.List)
		}
		if s.Else != nil {
			addStmt(g, fset, src, id, fileID, s.Else)
		}

	case *ast.SwitchStmt:
		id := stmtID("switch")
		tag := ""
		if s.Tag != nil {
			tag = extractText(fset, src, s.Tag.Pos(), s.Tag.End())
		}
		header := "switch"
		if tag != "" {
			header += " " + tag
		}
		addNode(id, graph.KindSwitch, "", map[string]string{"tag": tag, "src": header})
		if s.Body != nil {
			for _, c := range s.Body.List {
				if cc, ok := c.(*ast.CaseClause); ok {
					addCaseClause(g, fset, src, id, fileID, cc)
				}
			}
		}

	case *ast.TypeSwitchStmt:
		id := stmtID("switch")
		assign := extractText(fset, src, s.Assign.Pos(), s.Assign.End())
		addNode(id, graph.KindSwitch, "", map[string]string{
			"type_switch": "true",
			"assign":      assign,
			"src":         "switch " + assign,
		})
		if s.Body != nil {
			for _, c := range s.Body.List {
				if cc, ok := c.(*ast.CaseClause); ok {
					addCaseClause(g, fset, src, id, fileID, cc)
				}
			}
		}

	case *ast.SelectStmt:
		id := stmtID("select")
		addNode(id, graph.KindSelect, "", map[string]string{"src": "select"})
		if s.Body != nil {
			for _, c := range s.Body.List {
				if cc, ok := c.(*ast.CommClause); ok {
					addCommClause(g, fset, src, id, fileID, cc)
				}
			}
		}

	case *ast.ReturnStmt:
		id := stmtID("return")
		var valueTexts []string
		for _, v := range s.Results {
			valueTexts = append(valueTexts, extractText(fset, src, v.Pos(), v.End()))
		}
		retSrc := "return"
		if len(valueTexts) > 0 {
			retSrc += " " + strings.Join(valueTexts, ", ")
		}
		// De-indent multiline return values (closures, etc.) to be relative
		// to the function body, not absolute.
		retSrc = deindentSrc(retSrc, pos.Column-1)
		addNode(id, graph.KindReturn, "", map[string]string{
			"values": strings.Join(valueTexts, ", "),
			"src":    retSrc,
		})

	case *ast.DeferStmt:
		id := stmtID("defer")
		addClosureOrLeaf(g, fset, src, parentID, fileID, id, graph.KindDefer, "defer", s.Call, line, endLine, addNode)

	case *ast.GoStmt:
		id := stmtID("go")
		addClosureOrLeaf(g, fset, src, parentID, fileID, id, graph.KindGo, "go", s.Call, line, endLine, addNode)

	case *ast.AssignStmt:
		id := stmtID("assign")
		op := s.Tok.String()
		var lhsParts []string
		for _, l := range s.Lhs {
			lhsParts = append(lhsParts, extractText(fset, src, l.Pos(), l.End()))
		}
		var rhsParts []string
		for _, r := range s.Rhs {
			rhsParts = append(rhsParts, extractText(fset, src, r.Pos(), r.End()))
		}
		assignSrc := strings.Join(lhsParts, ", ") + " " + op + " " + strings.Join(rhsParts, ", ")
		addNode(id, graph.KindAssign, "", map[string]string{
			"op":  op,
			"lhs": strings.Join(lhsParts, ", "),
			"rhs": strings.Join(rhsParts, ", "),
			"src": assignSrc,
		})

	case *ast.SendStmt:
		id := stmtID("send")
		ch := extractText(fset, src, s.Chan.Pos(), s.Chan.End())
		val := extractText(fset, src, s.Value.Pos(), s.Value.End())
		addNode(id, graph.KindSend, "", map[string]string{
			"ch":  ch,
			"val": val,
			"src": ch + " <- " + val,
		})

	case *ast.BranchStmt:
		id := stmtID("branch")
		label := ""
		if s.Label != nil {
			label = s.Label.Name
		}
		branchSrc := s.Tok.String()
		if label != "" {
			branchSrc += " " + label
		}
		addNode(id, graph.KindBranch, "", map[string]string{
			"tok":   s.Tok.String(),
			"label": label,
			"src":   branchSrc,
		})

	case *ast.BlockStmt:
		// Anonymous block — recurse directly without creating an intermediate node.
		if s != nil {
			walkStatements(g, fset, src, parentID, fileID, s.List)
		}

	case *ast.ExprStmt:
		// Bare expression statements (function calls, type assertions, etc.)
		// Store the source for codegen reconstruction.
		id := stmtID("expr")
		exprSrc := extractText(fset, src, s.X.Pos(), s.X.End())
		addNode(id, graph.KindStatement, "", map[string]string{
			"src": exprSrc,
		})
		// Walk the expression for call expressions and link existing call nodes
		// to this statement via EdgeContains.
		ast.Inspect(s.X, func(n ast.Node) bool {
			callExpr, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			callPos := fset.Position(callExpr.Pos())
			// Call nodes are keyed by owner+index; find by file+line match.
			for _, e := range g.EdgesFrom(parentID) {
				if e.Kind != graph.EdgeContains {
					continue
				}
				cn, exists := g.GetNode(e.To)
				if !exists || cn.Kind != graph.KindCall {
					continue
				}
				if cn.Line == callPos.Line {
					_ = g.AddEdge(id, cn.ID, graph.EdgeContains)
				}
			}
			return true
		})

	case *ast.IncDecStmt:
		id := stmtID("assign")
		lhs := extractText(fset, src, s.X.Pos(), s.X.End())
		addNode(id, graph.KindAssign, "", map[string]string{
			"op":  s.Tok.String(),
			"lhs": lhs,
			"src": lhs + s.Tok.String(),
		})

	case *ast.DeclStmt:
		// var/const/type declarations inside functions — store as a statement with src.
		id := stmtID("decl")
		declSrc := extractText(fset, src, s.Pos(), s.End())
		addNode(id, graph.KindStatement, "", map[string]string{
			"src": declSrc,
		})
	}
}

// deindentSrc strips `tabs` leading tab characters from continuation lines
// (lines after the first) to convert absolute indentation to relative.
// The first line is left untouched.
func deindentSrc(s string, tabs int) string {
	if tabs <= 0 || !strings.Contains(s, "\n") {
		return s
	}
	prefix := strings.Repeat("\t", tabs)
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.TrimPrefix(lines[i], prefix)
	}
	return strings.Join(lines, "\n")
}

// addClosureOrLeaf handles defer/go statements. If the call contains a FuncLit
// (closure), the closure body is walked as children and the src stores just the
// header + closing suffix. Otherwise it's a simple leaf node.
func addClosureOrLeaf(g *graph.Graph, fset *token.FileSet, src []byte, parentID, fileID, id string, kind graph.NodeKind, keyword string, call *ast.CallExpr, line, endLine int, addNode func(string, graph.NodeKind, string, map[string]string)) {
	// Check if the call target is a FuncLit (closure).
	if fl, ok := call.Fun.(*ast.FuncLit); ok && fl.Body != nil {
		// Header: "defer func(params) rettype"
		header := keyword + " " + extractText(fset, src, fl.Pos(), fl.Body.Lbrace)
		header = strings.TrimRight(header, " {")

		// Closing suffix: "}(args)" — the arguments after the closure body.
		closeSuffix := "}"
		if len(call.Args) > 0 {
			argsText := extractText(fset, src, call.Lparen, call.Rparen+1)
			closeSuffix += argsText
		} else {
			closeSuffix += "()"
		}

		addNode(id, kind, "", map[string]string{
			"closure":      "true",
			"src":          header,
			"close_suffix": closeSuffix,
		})
		walkStatements(g, fset, src, id, fileID, fl.Body.List)
		return
	}

	// Simple call (no closure).
	callText := extractText(fset, src, call.Pos(), call.End())
	addNode(id, kind, "", map[string]string{
		"call": callText,
		"src":  keyword + " " + callText,
	})
}

func addCaseClause(g *graph.Graph, fset *token.FileSet, src []byte, parentID, fileID string, cc *ast.CaseClause) {
	pos := fset.Position(cc.Pos())
	id := fmt.Sprintf("case:%s:%d", fileID, pos.Line)
	caseText := ""
	if cc.List != nil {
		var parts []string
		for _, e := range cc.List {
			parts = append(parts, extractText(fset, src, e.Pos(), e.End()))
		}
		caseText = strings.Join(parts, ", ")
	}
	caseSrc := "default:"
	if caseText != "" {
		caseSrc = "case " + caseText + ":"
	}
	_ = g.AddNode(&graph.Node{
		ID:      id,
		Kind:    graph.KindStatement,
		Name:    "case",
		File:    fileID,
		Line:    pos.Line,
		EndLine: fset.Position(cc.End()).Line,
		Metadata: map[string]string{
			"case": caseText,
			"src":  caseSrc,
		},
	})
	_ = g.AddEdge(parentID, id, graph.EdgeContains)
	walkStatements(g, fset, src, id, fileID, cc.Body)
}

func addCommClause(g *graph.Graph, fset *token.FileSet, src []byte, parentID, fileID string, cc *ast.CommClause) {
	pos := fset.Position(cc.Pos())
	id := fmt.Sprintf("case:%s:%d", fileID, pos.Line)
	comm := ""
	if cc.Comm != nil {
		comm = extractText(fset, src, cc.Comm.Pos(), cc.Comm.End())
	}
	commSrc := "default:"
	if comm != "" {
		commSrc = "case " + comm + ":"
	}
	_ = g.AddNode(&graph.Node{
		ID:      id,
		Kind:    graph.KindStatement,
		Name:    "case",
		File:    fileID,
		Line:    pos.Line,
		EndLine: fset.Position(cc.End()).Line,
		Metadata: map[string]string{
			"comm": comm,
			"src":  commSrc,
		},
	})
	_ = g.AddEdge(parentID, id, graph.EdgeContains)
	// The comm clause itself (e.g. ch <- val or v := <-ch) is a statement — parse it too.
	if cc.Comm != nil {
		addStmt(g, fset, src, id, fileID, cc.Comm)
	}
	walkStatements(g, fset, src, id, fileID, cc.Body)
}


