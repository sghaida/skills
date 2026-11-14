package main

import (
	"go/ast"
	"go/token"
	"strings"
)

func countGotos(body *ast.BlockStmt) int {
	count := 0
	ast.Inspect(body, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		if branch, ok := node.(*ast.BranchStmt); ok && branch.Tok == token.GOTO {
			count++
		}
		return true
	})
	return count
}

func collectRecursionMetrics(fn *ast.FuncDecl, report *FunctionReport) {
	report.RecursionDirective, report.RecursionJustified = recursionDirective(fn.Doc)
	receiver := recursionReceiver(fn)
	bindings := collectRecursionBindings(fn, receiver)
	if receiver != nil && receiverReassigned(fn.Body, bindings) {
		return
	}
	asynchronous := make(map[*ast.CallExpr]bool)
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.GoStmt:
			asynchronous[n.Call] = true
		case *ast.CallExpr:
			if !asynchronous[n] && isSelfCall(n.Fun, fn, receiver, bindings) {
				report.RecursiveCalls++
			}
		}
		return true
	})
}

func recursionReceiver(fn *ast.FuncDecl) *ast.Ident {
	if fn.Recv == nil || len(fn.Recv.List) != 1 || len(fn.Recv.List[0].Names) != 1 {
		return nil
	}
	return fn.Recv.List[0].Names[0]
}

func receiverReassigned(body *ast.BlockStmt, bindings *recursionBindings) bool {
	reassigned := false
	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				reassigned = reassigned || bindings.assignsReceiver(lhs)
			}
		case *ast.RangeStmt:
			if n.Tok == token.ASSIGN {
				reassigned = reassigned || bindings.assignsReceiver(n.Key) || bindings.assignsReceiver(n.Value)
			}
		case *ast.IncDecStmt:
			reassigned = reassigned || bindings.assignsReceiver(n.X)
		}
		return true
	})
	return reassigned
}

func isSelfCall(expr ast.Expr, fn *ast.FuncDecl, receiver *ast.Ident, bindings *recursionBindings) bool {
	switch callee := ast.Unparen(expr).(type) {
	case *ast.IndexExpr:
		return isSelfCall(callee.X, fn, receiver, bindings)
	case *ast.IndexListExpr:
		return isSelfCall(callee.X, fn, receiver, bindings)
	case *ast.Ident:
		return fn.Recv == nil && callee.Name == fn.Name.Name && !bindings.shadowed(callee.Pos())
	case *ast.SelectorExpr:
		base, ok := ast.Unparen(callee.X).(*ast.Ident)
		return ok && receiver != nil && base.Name == receiver.Name &&
			!bindings.shadowed(base.Pos()) && callee.Sel.Name == fn.Name.Name
	default:
		return false
	}
}

func recursionDirective(doc *ast.CommentGroup) (present, justified bool) {
	if doc == nil {
		return false, false
	}
	const directive = "chnq:recursion"
	for _, comment := range doc.List {
		text := strings.TrimPrefix(comment.Text, "//")
		text = strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
			switch {
			case line == directive:
				present = true
			case strings.HasPrefix(line, directive+" ") || strings.HasPrefix(line, directive+"\t"):
				present = true
				justified = justified || strings.TrimSpace(line[len(directive):]) != ""
			case present:
				if line == "" || strings.HasPrefix(line, "chnq:") {
					return present, justified
				}
				justified = true
			}
		}
	}
	return present, justified
}
