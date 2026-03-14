package parser

import (
	"go/ast"
	"go/token"
)

// CyclomaticComplexity computes the cyclomatic complexity of a function body.
// It counts decision points: if, for, range, case, &&, ||, plus 1 for the
// function entry. This matches gocyclo's methodology.
func CyclomaticComplexity(body *ast.BlockStmt) int {
	if body == nil {
		return 1
	}
	complexity := 1
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.IfStmt:
			complexity++
		case *ast.ForStmt:
			complexity++
		case *ast.RangeStmt:
			complexity++
		case *ast.CaseClause:
			// Each case (except default) is a decision point.
			if n.List != nil {
				complexity++
			}
		case *ast.CommClause:
			// Each select case (except default) is a decision point.
			if n.Comm != nil {
				complexity++
			}
		case *ast.BinaryExpr:
			if n.Op == token.LAND || n.Op == token.LOR {
				complexity++
			}
		}
		return true
	})
	return complexity
}

// CognitiveComplexity computes the cognitive complexity of a function body.
// It measures how hard a function is to understand, based on nesting depth
// and control flow breaks. This matches gocognit's methodology.
//
// Rules:
//   - +1 for each: if, else if, else, for, range, switch, select, &&, ||, goto, break/continue with label
//   - +nesting for each: if, for, range, switch, select (nested increments are cumulative)
//   - Nesting increments for: function literals (closures)
func CognitiveComplexity(body *ast.BlockStmt) int {
	if body == nil {
		return 0
	}
	return cognitiveWalk(body, 0)
}

func cognitiveWalk(node ast.Node, nesting int) int {
	complexity := 0

	ast.Inspect(node, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		switch s := n.(type) {
		case *ast.IfStmt:
			// +1 for if, +nesting for depth.
			complexity += 1 + nesting
			// Walk condition for && / ||.
			complexity += countBoolOps(s.Cond)
			// Walk body at nesting+1.
			complexity += cognitiveWalk(s.Body, nesting+1)
			// Else branch.
			if s.Else != nil {
				switch e := s.Else.(type) {
				case *ast.IfStmt:
					// else if: +1 (no nesting increment).
					complexity += 1
					complexity += countBoolOps(e.Cond)
					complexity += cognitiveWalk(e.Body, nesting+1)
					if e.Else != nil {
						complexity += cognitiveElse(e.Else, nesting)
					}
				case *ast.BlockStmt:
					// else: +1.
					complexity += 1
					complexity += cognitiveWalk(e, nesting+1)
				}
			}
			return false // already walked children

		case *ast.ForStmt:
			complexity += 1 + nesting
			if s.Cond != nil {
				complexity += countBoolOps(s.Cond)
			}
			complexity += cognitiveWalk(s.Body, nesting+1)
			return false

		case *ast.RangeStmt:
			complexity += 1 + nesting
			complexity += cognitiveWalk(s.Body, nesting+1)
			return false

		case *ast.SwitchStmt:
			complexity += 1 + nesting
			complexity += cognitiveWalk(s.Body, nesting+1)
			return false

		case *ast.TypeSwitchStmt:
			complexity += 1 + nesting
			complexity += cognitiveWalk(s.Body, nesting+1)
			return false

		case *ast.SelectStmt:
			complexity += 1 + nesting
			complexity += cognitiveWalk(s.Body, nesting+1)
			return false

		case *ast.FuncLit:
			// Closures increase nesting.
			complexity += cognitiveWalk(s.Body, nesting+1)
			return false

		case *ast.BranchStmt:
			// break/continue/goto with label.
			if s.Label != nil {
				complexity++
			}
		}
		return true
	})

	return complexity
}

func cognitiveElse(node ast.Node, nesting int) int {
	complexity := 0
	switch e := node.(type) {
	case *ast.IfStmt:
		complexity += 1
		complexity += countBoolOps(e.Cond)
		complexity += cognitiveWalk(e.Body, nesting+1)
		if e.Else != nil {
			complexity += cognitiveElse(e.Else, nesting)
		}
	case *ast.BlockStmt:
		complexity += 1
		complexity += cognitiveWalk(e, nesting+1)
	}
	return complexity
}

// countBoolOps counts sequences of && and || in an expression.
// Each change in boolean operator type adds 1.
func countBoolOps(expr ast.Expr) int {
	count := 0
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		b, ok := e.(*ast.BinaryExpr)
		if !ok {
			return
		}
		if b.Op == token.LAND || b.Op == token.LOR {
			count++
		}
		walk(b.X)
		walk(b.Y)
	}
	walk(expr)
	return count
}
