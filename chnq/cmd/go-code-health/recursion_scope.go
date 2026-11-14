package main

import (
	"go/ast"
	"go/token"
)

type recursionBindingSpan struct {
	start token.Pos
	end   token.Pos
}

type recursionBindings struct {
	name        string
	body        *ast.BlockStmt
	receiver    bool
	spans       []recursionBindingSpan
	definitions map[token.Pos]bool
}

func collectRecursionBindings(fn *ast.FuncDecl, receiver *ast.Ident) *recursionBindings {
	bindings := &recursionBindings{
		name: fn.Name.Name, body: fn.Body, receiver: receiver != nil,
		definitions: make(map[token.Pos]bool),
	}
	if receiver != nil {
		bindings.name = receiver.Name
	}
	bindings.shadowFields(fn.Type.TypeParams, fn.Body.Pos(), fn.Body.End())
	bindings.shadowFields(fn.Type.Params, fn.Body.Pos(), fn.Body.End())
	bindings.shadowFields(fn.Type.Results, fn.Body.Pos(), fn.Body.End())
	ast.Walk(recursionBindingVisitor{bindings: bindings, scope: fn.Body}, fn.Body)
	return bindings
}

func (b *recursionBindings) shadowFields(fields *ast.FieldList, start, end token.Pos) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			b.add(name, start, end)
		}
	}
}

func (b *recursionBindings) add(name *ast.Ident, start, end token.Pos) {
	if name != nil && name.Name == b.name {
		b.spans = append(b.spans, recursionBindingSpan{start: start, end: end})
	}
}

func (b *recursionBindings) shadowed(pos token.Pos) bool {
	for _, span := range b.spans {
		if span.start <= pos && pos < span.end {
			return true
		}
	}
	return false
}

func (b *recursionBindings) assignsReceiver(expr ast.Expr) bool {
	name, ok := ast.Unparen(expr).(*ast.Ident)
	return ok && name.Name == b.name && !b.definitions[name.Pos()] && !b.shadowed(name.Pos())
}

// Value visitors retain the enclosing scope for siblings without a mutable
// scope stack. Only bindings for the candidate function/receiver are tracked.
type recursionBindingVisitor struct {
	bindings   *recursionBindings
	scope      ast.Node
	typeSwitch *ast.TypeSwitchStmt
}

func (v recursionBindingVisitor) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case nil:
		return nil
	case *ast.BlockStmt, *ast.IfStmt, *ast.ForStmt, *ast.SwitchStmt,
		*ast.SelectStmt, *ast.CaseClause, *ast.CommClause:
		v.scope = node
	case *ast.TypeSwitchStmt:
		v.scope, v.typeSwitch = n, n
	case *ast.RangeStmt:
		v.scope = n
		if n.Tok == token.DEFINE {
			for _, expr := range []ast.Expr{n.Key, n.Value} {
				if name, ok := expr.(*ast.Ident); ok {
					v.bindings.add(name, n.Body.Pos(), n.Body.End())
				}
			}
		}
	case *ast.AssignStmt:
		v.shortDeclaration(n)
	case *ast.ValueSpec:
		for _, name := range n.Names {
			v.bindings.add(name, n.End(), v.scope.End())
		}
	case *ast.TypeSpec:
		v.bindings.add(n.Name, n.Name.Pos(), v.scope.End())
		v.bindings.shadowFields(n.TypeParams, n.Name.End(), n.End())
	case *ast.FuncLit:
		v.bindings.shadowFields(n.Type.Params, n.Body.Pos(), n.Body.End())
		v.bindings.shadowFields(n.Type.Results, n.Body.Pos(), n.Body.End())
	}
	return v
}

func (v recursionBindingVisitor) shortDeclaration(assignment *ast.AssignStmt) {
	if assignment.Tok != token.DEFINE || (v.bindings.receiver && v.scope == v.bindings.body) {
		return
	}
	for _, lhs := range assignment.Lhs {
		name, ok := lhs.(*ast.Ident)
		if !ok || name.Name != v.bindings.name {
			continue
		}
		v.bindings.definitions[name.Pos()] = true
		if v.typeSwitch != nil && v.typeSwitch.Assign == assignment {
			// A type-switch guard variable exists in clause bodies, not case types.
			for _, statement := range v.typeSwitch.Body.List {
				clause, ok := statement.(*ast.CaseClause)
				if ok {
					v.bindings.add(name, clause.Colon+1, clause.End())
				}
			}
			continue
		}
		v.bindings.add(name, assignment.End(), v.scope.End())
	}
}
