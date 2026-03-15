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
		addNode(id, graph.KindFor, extractText(fset, src, s.Pos(), s.End()), map[string]string{
			"cond": cond,
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
		meta["over"] = extractText(fset, src, s.X.Pos(), s.X.End())
		addNode(id, graph.KindFor, extractText(fset, src, s.Pos(), s.End()), meta)
		if s.Body != nil {
			walkStatements(g, fset, src, id, fileID, s.Body.List)
		}

	case *ast.IfStmt:
		id := stmtID("if")
		cond := extractText(fset, src, s.Cond.Pos(), s.Cond.End())
		meta := map[string]string{"cond": cond}
		if s.Else != nil {
			meta["has_else"] = "true"
		}
		addNode(id, graph.KindIf, cond, meta)
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
		addNode(id, graph.KindSwitch, tag, map[string]string{"tag": tag})
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
		addNode(id, graph.KindSwitch, assign, map[string]string{
			"type_switch": "true",
			"assign":      assign,
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
		addNode(id, graph.KindSelect, "", nil)
		if s.Body != nil {
			for _, c := range s.Body.List {
				if cc, ok := c.(*ast.CommClause); ok {
					addCommClause(g, fset, src, id, fileID, cc)
				}
			}
		}

	case *ast.ReturnStmt:
		id := stmtID("return")
		vals := extractText(fset, src, s.Pos(), s.End())
		var valueTexts []string
		for _, v := range s.Results {
			valueTexts = append(valueTexts, extractText(fset, src, v.Pos(), v.End()))
		}
		addNode(id, graph.KindReturn, vals, map[string]string{
			"values": strings.Join(valueTexts, ", "),
		})

	case *ast.DeferStmt:
		id := stmtID("defer")
		call := extractText(fset, src, s.Call.Pos(), s.Call.End())
		addNode(id, graph.KindDefer, call, map[string]string{"call": call})

	case *ast.GoStmt:
		id := stmtID("go")
		call := extractText(fset, src, s.Call.Pos(), s.Call.End())
		addNode(id, graph.KindGo, call, map[string]string{"call": call})

	case *ast.AssignStmt:
		id := stmtID("assign")
		op := s.Tok.String()
		var lhsParts []string
		for _, l := range s.Lhs {
			lhsParts = append(lhsParts, extractText(fset, src, l.Pos(), l.End()))
		}
		addNode(id, graph.KindAssign, extractText(fset, src, s.Pos(), s.End()), map[string]string{
			"op":  op,
			"lhs": strings.Join(lhsParts, ", "),
		})

	case *ast.SendStmt:
		id := stmtID("send")
		ch := extractText(fset, src, s.Chan.Pos(), s.Chan.End())
		val := extractText(fset, src, s.Value.Pos(), s.Value.End())
		addNode(id, graph.KindSend, extractText(fset, src, s.Pos(), s.End()), map[string]string{
			"ch":  ch,
			"val": val,
		})

	case *ast.BranchStmt:
		id := stmtID("branch")
		label := ""
		if s.Label != nil {
			label = s.Label.Name
		}
		addNode(id, graph.KindBranch, s.Tok.String(), map[string]string{
			"tok":   s.Tok.String(),
			"label": label,
		})

	case *ast.BlockStmt:
		// Anonymous block — recurse directly without creating an intermediate node.
		if s != nil {
			walkStatements(g, fset, src, parentID, fileID, s.List)
		}

	case *ast.ExprStmt:
		// Expression statements (bare function calls, etc.) are already captured
		// as KindCall nodes by the existing addCallNode walker — skip.

	case *ast.IncDecStmt:
		id := stmtID("assign")
		addNode(id, graph.KindAssign, extractText(fset, src, s.Pos(), s.End()), map[string]string{
			"op": s.Tok.String(),
		})

	case *ast.DeclStmt:
		// var/const/type declarations inside functions.
		if gd, ok := s.Decl.(*ast.GenDecl); ok {
			walkStatements(g, fset, src, parentID, fileID, declToStmts(fset, src, gd))
		}
	}
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
	_ = g.AddNode(&graph.Node{
		ID:      id,
		Kind:    graph.KindStatement,
		Name:    "case",
		File:    fileID,
		Line:    pos.Line,
		EndLine: fset.Position(cc.End()).Line,
		Text:    caseText,
		Metadata: map[string]string{
			"case": caseText,
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
	_ = g.AddNode(&graph.Node{
		ID:      id,
		Kind:    graph.KindStatement,
		Name:    "case",
		File:    fileID,
		Line:    pos.Line,
		EndLine: fset.Position(cc.End()).Line,
		Text:    comm,
		Metadata: map[string]string{
			"comm": comm,
		},
	})
	_ = g.AddEdge(parentID, id, graph.EdgeContains)
	// The comm clause itself (e.g. ch <- val or v := <-ch) is a statement — parse it too.
	if cc.Comm != nil {
		addStmt(g, fset, src, id, fileID, cc.Comm)
	}
	walkStatements(g, fset, src, id, fileID, cc.Body)
}

// declToStmts is a no-op adapter — inline var/const/type declarations inside
// functions don't produce statement nodes (they're already covered by type/var
// nodes at the file level). Returning nil skips them.
func declToStmts(_ *token.FileSet, _ []byte, _ *ast.GenDecl) []ast.Stmt {
	return nil
}

