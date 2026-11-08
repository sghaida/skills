package main

import (
	"go/ast"
	"go/token"
)

const defaultBroadInterfaceThreshold = 5

type interfaceInfo struct {
	name        string
	methods     map[string]bool
	line        int
	packagePath string
	used        bool
}

func collectArchitectureMetadata(parsed *ast.File, file *sourceFile, fset *token.FileSet) {
	file.typeMethods = make(map[string]map[string]bool)
	for _, decl := range parsed.Decls {
		switch declaration := decl.(type) {
		case *ast.GenDecl:
			collectInterfaceDeclarations(declaration, file, fset)
		case *ast.FuncDecl:
			collectReceiverMethod(declaration, file)
		}
	}
	markUsedInterfaces(parsed, file)
}

func collectInterfaceDeclarations(declaration *ast.GenDecl, file *sourceFile, fset *token.FileSet) {
	for _, spec := range declaration.Specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		interfaceType, ok := typeSpec.Type.(*ast.InterfaceType)
		if !ok {
			continue
		}
		info := interfaceInfo{
			name: typeSpec.Name.Name, methods: make(map[string]bool),
			line: fset.Position(typeSpec.Pos()).Line, packagePath: file.packagePath,
		}
		for _, field := range interfaceType.Methods.List {
			if len(field.Names) == 1 {
				info.methods[field.Names[0].Name] = true
			}
		}
		file.interfaces = append(file.interfaces, info)
	}
}

func collectReceiverMethod(declaration *ast.FuncDecl, file *sourceFile) {
	if declaration.Recv == nil || len(declaration.Recv.List) == 0 {
		return
	}
	receiver := receiverName(declaration.Recv.List[0].Type)
	if receiver == "" {
		return
	}
	if file.typeMethods[receiver] == nil {
		file.typeMethods[receiver] = make(map[string]bool)
	}
	file.typeMethods[receiver][declaration.Name.Name] = true
}

func receiverName(expr ast.Expr) string {
	switch receiver := expr.(type) {
	case *ast.Ident:
		return receiver.Name
	case *ast.StarExpr:
		return receiverName(receiver.X)
	case *ast.IndexExpr:
		return receiverName(receiver.X)
	case *ast.IndexListExpr:
		return receiverName(receiver.X)
	default:
		return ""
	}
}

func markUsedInterfaces(parsed *ast.File, file *sourceFile) {
	declared := make(map[string]bool, len(file.interfaces))
	declarationPositions := make(map[token.Pos]bool, len(file.interfaces))
	for _, info := range file.interfaces {
		declared[info.name] = true
	}
	for _, decl := range parsed.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if ok && declared[typeSpec.Name.Name] {
				declarationPositions[typeSpec.Name.Pos()] = true
			}
		}
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok || !declared[ident.Name] {
			return true
		}
		for i := range file.interfaces {
			if file.interfaces[i].name == ident.Name && !declarationPositions[ident.Pos()] {
				file.interfaces[i].used = true
			}
		}
		return true
	})
}

func analyzeArchitecture(files []*sourceFile, broadThreshold int) {
	if broadThreshold < 1 {
		broadThreshold = defaultBroadInterfaceThreshold
	}
	methodsByPackage := make(map[string]map[string]map[string]bool)
	for _, file := range files {
		file.cycleScore = 10
		if file.packageImportCycles > 0 {
			file.cycleScore = 0
		}
		for name, methods := range file.typeMethods {
			if methodsByPackage[file.packagePath] == nil {
				methodsByPackage[file.packagePath] = make(map[string]map[string]bool)
			}
			methodsByPackage[file.packagePath][name] = mergeMethodSets(
				methodsByPackage[file.packagePath][name], methods,
			)
		}
	}

	for _, file := range files {
		total, unused, broad, matched := 0, 0, 0, 0
		for _, info := range file.interfaces {
			total++
			if !info.used {
				unused++
			}
			if len(info.methods) > broadThreshold {
				broad++
			}
			if interfaceHasImplementation(info, methodsByPackage[info.packagePath]) {
				matched++
			}
		}
		file.interfaceScore = interfaceComplianceScore(total, unused, broad)
		file.duckTypingScore = duckTypingScore(total, matched)
	}
}

func collectArchitectureHealth(files []*sourceFile, summary *Summary, broadThreshold int) {
	for _, file := range files {
		interfaces, unused, broad, implicit := architectureHealth(file, broadThreshold)
		summary.Health.InterfaceCount += interfaces
		summary.Health.UnusedInterfaces += unused
		summary.Health.BroadInterfaces += broad
		summary.Health.ImplicitImplementations += implicit
	}
}

func mergeMethodSets(current, next map[string]bool) map[string]bool {
	if current == nil {
		current = make(map[string]bool)
	}
	for method := range next {
		current[method] = true
	}
	return current
}

func interfaceHasImplementation(info interfaceInfo, methods map[string]map[string]bool) bool {
	if len(info.methods) == 0 {
		return true
	}
	for _, typeMethods := range methods {
		matches := true
		for method := range info.methods {
			if !typeMethods[method] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func interfaceComplianceScore(total, unused, broad int) float64 {
	if total == 0 {
		return 10
	}
	penalty := 6*float64(unused)/float64(total) + 4*float64(broad)/float64(total)
	return round1(clamp(10 - penalty))
}

func duckTypingScore(total, matched int) float64 {
	if total == 0 {
		return 10
	}
	return round1(10 * float64(matched) / float64(total))
}

func architectureHealth(file *sourceFile, broadThreshold int) (interfaces, unused, broad, implicit int) {
	interfaces = len(file.interfaces)
	for _, info := range file.interfaces {
		if !info.used {
			unused++
		}
		if len(info.methods) > broadThreshold {
			broad++
		}
	}
	for _, methods := range file.typeMethods {
		for _, info := range file.interfaces {
			if interfaceHasImplementation(info, map[string]map[string]bool{"type": methods}) {
				implicit++
			}
		}
	}
	return interfaces, unused, broad, implicit
}
