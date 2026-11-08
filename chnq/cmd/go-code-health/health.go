package main

import (
	"go/ast"
	"go/token"
	"strings"
)

// collecthealthmetrics handles collect health metrics
func collectHealthMetrics(parsed *ast.File, file *sourceFile) {
	countWorkMarkers(parsed, file)
	for _, decl := range parsed.Decls {
		collectExportedDeclaration(decl, file)
	}
	collectRuntimeSignals(parsed, file)
}

// countworkmarkers handles count work markers
func countWorkMarkers(parsed *ast.File, file *sourceFile) {
	for _, group := range parsed.Comments {
		for _, comment := range group.List {
			countCommentMarkers(comment.Text, file)
		}
	}
}

// countcommentmarkers handles count comment markers
func countCommentMarkers(text string, file *sourceFile) {
	upper := strings.ToUpper(text)
	file.todos += strings.Count(upper, "TODO")
	file.fixmes += strings.Count(upper, "FIXME")
}

// collectruntimesignals handles collect runtime signals
func collectRuntimeSignals(parsed *ast.File, file *sourceFile) {
	ast.Inspect(parsed, func(node ast.Node) bool {
		collectPanicSignal(node, file)
		collectGoroutineSignal(node, file)
		collectChannelSignal(node, file)
		collectIgnoredReturnSignal(node, file)
		return true
	})
}

// collectpanicsignal handles collect panic signal
func collectPanicSignal(node ast.Node, file *sourceFile) {
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return
	}
	ident, ok := call.Fun.(*ast.Ident)
	if ok && ident.Name == "panic" {
		file.panics++
	}
}

// collectgoroutinesignal handles collect goroutine signal
func collectGoroutineSignal(node ast.Node, file *sourceFile) {
	if _, ok := node.(*ast.GoStmt); ok {
		file.goroutines++
	}
}

// collectchannelsignal handles collect channel signal
func collectChannelSignal(node ast.Node, file *sourceFile) {
	switch n := node.(type) {
	case *ast.SendStmt:
		file.channelOperations++
	case *ast.UnaryExpr:
		if n.Op == token.ARROW {
			file.channelOperations++
		}
	}
}

// collectignoredreturnsignal handles collect ignored return signal
func collectIgnoredReturnSignal(node ast.Node, file *sourceFile) {
	assignment, ok := node.(*ast.AssignStmt)
	if ok {
		countIgnoredReturnValues(assignment, file)
	}
}

// collectexporteddeclaration handles collect exported declaration
func collectExportedDeclaration(decl ast.Decl, file *sourceFile) {
	switch declaration := decl.(type) {
	case *ast.FuncDecl:
		collectExportedFunction(declaration, file)
	case *ast.GenDecl:
		collectExportedSpecs(declaration, file)
	}
}

// collectexportedfunction handles collect exported function
func collectExportedFunction(declaration *ast.FuncDecl, file *sourceFile) {
	hasDoc := func(doc *ast.CommentGroup) bool {
		return doc != nil && strings.TrimSpace(doc.Text()) != ""
	}
	if ast.IsExported(declaration.Name.Name) {
		file.exportedSymbols++
		if hasDoc(declaration.Doc) {
			file.documentedExportedSymbols++
		}
	}
}

// collectexportedspecs handles collect exported specs
func collectExportedSpecs(declaration *ast.GenDecl, file *sourceFile) {
	for _, spec := range declaration.Specs {
		names := exportedSpecNames(spec)
		for range names {
			file.exportedSymbols++
			if hasSpecDocumentation(declaration, spec) {
				file.documentedExportedSymbols++
			}
		}
	}
}

// exportedspecnames handles exported spec names
func exportedSpecNames(spec ast.Spec) []*ast.Ident {
	switch specification := spec.(type) {
	case *ast.TypeSpec:
		return exportedTypeName(specification)
	case *ast.ValueSpec:
		return exportedValueNames(specification)
	}
	return nil
}

// exportedtypename handles exported type name
func exportedTypeName(specification *ast.TypeSpec) []*ast.Ident {
	if !ast.IsExported(specification.Name.Name) {
		return nil
	}
	return []*ast.Ident{specification.Name}
}

// exportedvaluenames handles exported value names
func exportedValueNames(specification *ast.ValueSpec) []*ast.Ident {
	names := make([]*ast.Ident, 0, len(specification.Names))
	for _, name := range specification.Names {
		if ast.IsExported(name.Name) {
			names = append(names, name)
		}
	}
	return names
}

// hasspecdocumentation reports whether has spec documentation
func hasSpecDocumentation(declaration *ast.GenDecl, spec ast.Spec) bool {
	var doc *ast.CommentGroup
	if declaration.Doc != nil {
		doc = declaration.Doc
	}
	if valueSpec, ok := spec.(*ast.ValueSpec); ok && valueSpec.Doc != nil {
		doc = valueSpec.Doc
	}
	return doc != nil && strings.TrimSpace(doc.Text()) != ""
}

// countignoredreturnvalues handles count ignored return values
func countIgnoredReturnValues(assignment *ast.AssignStmt, file *sourceFile) {
	for i, lhs := range assignment.Lhs {
		if ignoredCallAt(assignment, lhs, i) {
			file.ignoredReturnValues++
		}
	}
}

// ignoredcallat handles ignored call at
func ignoredCallAt(assignment *ast.AssignStmt, lhs ast.Expr, index int) bool {
	ident, ok := lhs.(*ast.Ident)
	if !ok || ident.Name != "_" || index >= len(assignment.Rhs) {
		return false
	}
	_, ok = assignment.Rhs[index].(*ast.CallExpr)
	return ok
}
