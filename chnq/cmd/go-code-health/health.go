package main

import (
	"go/ast"
	"go/token"
	"strings"
)

func collectHealthMetrics(parsed *ast.File, file *sourceFile) {
	countWorkMarkers(parsed, file)
	for _, decl := range parsed.Decls {
		collectExportedDeclaration(decl, file)
	}
	collectRuntimeSignals(parsed, file)
}

func countWorkMarkers(parsed *ast.File, file *sourceFile) {
	for _, group := range parsed.Comments {
		for _, comment := range group.List {
			text := strings.ToUpper(comment.Text)
			if strings.Contains(text, "TODO") {
				file.todos++
			}
			if strings.Contains(text, "FIXME") {
				file.fixmes++
			}
		}
	}
}

func collectRuntimeSignals(parsed *ast.File, file *sourceFile) {
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			if ident, ok := n.Fun.(*ast.Ident); ok && ident.Name == "panic" {
				file.panics++
			}
		case *ast.GoStmt:
			file.goroutines++
		case *ast.SendStmt:
			file.channelOperations++
		case *ast.UnaryExpr:
			if n.Op == token.ARROW {
				file.channelOperations++
			}
		case *ast.AssignStmt:
			countIgnoredReturnValues(n, file)
		}
		return true
	})
}

func collectExportedDeclaration(decl ast.Decl, file *sourceFile) {
	switch declaration := decl.(type) {
	case *ast.FuncDecl:
		collectExportedFunction(declaration, file)
	case *ast.GenDecl:
		collectExportedSpecs(declaration, file)
	}
}

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

func exportedSpecNames(spec ast.Spec) []*ast.Ident {
	switch specification := spec.(type) {
	case *ast.TypeSpec:
		return exportedTypeName(specification)
	case *ast.ValueSpec:
		return exportedValueNames(specification)
	}
	return nil
}

func exportedTypeName(specification *ast.TypeSpec) []*ast.Ident {
	if !ast.IsExported(specification.Name.Name) {
		return nil
	}
	return []*ast.Ident{specification.Name}
}

func exportedValueNames(specification *ast.ValueSpec) []*ast.Ident {
	names := make([]*ast.Ident, 0, len(specification.Names))
	for _, name := range specification.Names {
		if ast.IsExported(name.Name) {
			names = append(names, name)
		}
	}
	return names
}

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

func countIgnoredReturnValues(assignment *ast.AssignStmt, file *sourceFile) {
	for i, lhs := range assignment.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok || ident.Name != "_" || i >= len(assignment.Rhs) {
			continue
		}
		if _, ok := assignment.Rhs[i].(*ast.CallExpr); ok {
			file.ignoredReturnValues++
		}
	}
}
