package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var generatedHeader = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

// functiondata represents function data data
type functionData struct {
	report           FunctionReport
	tokens           []string
	duplicateMarkers []int
}

// sourcefile represents source file data
type sourceFile struct {
	path                       string
	packagePath                string
	packageName                string
	lines                      int
	imports                    []string
	funcs                      []*functionData
	internalImports            int
	packageFanOut              int
	packageFanIn               int
	packageDependencyDepth     int
	packageImportCycles        int
	exportedSymbols            int
	documentedExportedSymbols  int
	ignoredReturnValues        int
	panics                     int
	todos                      int
	fixmes                     int
	goroutines                 int
	channelOperations          int
	gitChurn                   int
	duplicateTokens            int
	externalDuplicationPercent *float64
	externalDuplicateTokens    int
	externalTotalTokens        int
	totalFunctionTokens        int
	cycleScore                 float64
	interfaceScore             float64
	duckTypingScore            float64
	interfaces                 []interfaceInfo
	typeMethods                map[string]map[string]bool
}

// linecount handles line count
func lineCount(src []byte) int {
	if len(src) == 0 {
		return 0
	}
	n := bytes.Count(src, []byte{'\n'})
	if src[len(src)-1] != '\n' {
		n++
	}
	return n
}

// scango handles scan go
func scanGo(path, relativePath string, src []byte) (*sourceFile, error) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	f := &sourceFile{
		path:        filepath.ToSlash(relativePath),
		packagePath: filepath.ToSlash(filepath.Dir(relativePath)),
		packageName: parsed.Name.Name,
		lines:       lineCount(src),
		funcs:       make([]*functionData, 0),
	}
	f.imports = collectImports(parsed.Imports)
	collectHealthMetrics(parsed, f)
	collectArchitectureMetadata(parsed, f, fset)
	collectFunctionDeclarations(f, fset, src, parsed.Decls)
	collectClosures(f, fset, src, parsed)
	sort.Slice(f.funcs, func(i, j int) bool { return f.funcs[i].report.Line < f.funcs[j].report.Line })
	return f, nil
}

// collectImports extracts import paths that can be unquoted.
func collectImports(specs []*ast.ImportSpec) []string {
	imports := make([]string, 0, len(specs))
	for _, spec := range specs {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err == nil {
			imports = append(imports, importPath)
		}
	}
	return imports
}

// collectFunctionDeclarations records top-level functions and methods.
func collectFunctionDeclarations(f *sourceFile, fset *token.FileSet, src []byte, declarations []ast.Decl) {
	for _, decl := range declarations {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil {
			name = "method:" + name
		}
		appendFunctionData(f, fset, src, name, fn.Body, fn.Pos(), fn.End())
		if fn.Body != nil {
			collectRecursionMetrics(fn, &f.funcs[len(f.funcs)-1].report)
		}
	}
}

// collectClosures records closures separately from their enclosing functions.
func collectClosures(f *sourceFile, fset *token.FileSet, src []byte, parsed *ast.File) {
	ast.Inspect(parsed, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			appendFunctionData(f, fset, src, fmt.Sprintf("closure@%d", fset.Position(lit.Pos()).Line),
				lit.Body, lit.Pos(), lit.End())
		}
		return true
	})
}

// appendfunctiondata handles append function data
func appendFunctionData(f *sourceFile, fset *token.FileSet, src []byte, name string, body *ast.BlockStmt, from, to token.Pos) {
	if body == nil {
		return
	}
	line := fset.Position(from).Line
	endLine := fset.Position(to).Line
	complexity, depth := measureBody(body)
	startOffset := fset.Position(body.Pos()).Offset
	endOffset := fset.Position(body.End()).Offset
	excluded := nestedFunctionSpans(body, fset, startOffset)
	tokens := scanTokens(src[startOffset:endOffset], excluded)
	f.funcs = append(f.funcs, &functionData{
		report: FunctionReport{
			Name:                name,
			Line:                line,
			Lines:               endLine - line + 1,
			BranchingComplexity: complexity,
			MaxNesting:          depth,
			GotoCount:           countGotos(body),
		},
		tokens: tokens,
	})
	f.totalFunctionTokens += len(tokens)
}

// nestedfunctionspans handles nested function spans
func nestedFunctionSpans(body *ast.BlockStmt, fset *token.FileSet, startOffset int) []tokenSpan {
	var excluded []tokenSpan
	ast.Inspect(body, func(n ast.Node) bool {
		lit, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}
		excluded = append(excluded, tokenSpan{
			start: fset.Position(lit.Body.Pos()).Offset - startOffset,
			end:   fset.Position(lit.Body.End()).Offset - startOffset,
		})
		return false
	})
	return excluded
}

// this is a documented branching/nesting heuristic, not sonar's cognitive complexity
func measureBody(body *ast.BlockStmt) (int, int) {
	if body == nil {
		return 0, 0
	}
	complexity, maxDepth := 0, 0
	var visit func(ast.Node, int)
	visit = func(node ast.Node, depth int) {
		if node == nil {
			return
		}
		if _, ok := node.(*ast.FuncLit); ok {
			return
		}
		increment, nextDepth := nodeComplexity(node, depth)
		complexity += increment
		if nextDepth > maxDepth {
			maxDepth = nextDepth
		}
		visitChildren(node, nextDepth, visit)
	}
	for _, statement := range body.List {
		visit(statement, 0)
	}
	return complexity, maxDepth
}

// nodecomplexity handles node complexity
func nodeComplexity(node ast.Node, depth int) (int, int) {
	if increment, nested := controlFlowComplexity(node, depth); nested {
		return increment, depth + 1
	}
	return nodeClauseComplexity(node), depth
}

// controlflowcomplexity handles control flow complexity
func controlFlowComplexity(node ast.Node, depth int) (int, bool) {
	switch node.(type) {
	case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
		return 1 + depth, true
	default:
		return 0, false
	}
}

// nodeclausecomplexity handles node clause complexity
func nodeClauseComplexity(node ast.Node) int {
	if complexity := caseClauseComplexity(node); complexity > 0 {
		return complexity
	}
	if complexity := commClauseComplexity(node); complexity > 0 {
		return complexity
	}
	return binaryClauseComplexity(node)
}

// caseclausecomplexity handles case clause complexity
func caseClauseComplexity(node ast.Node) int {
	clause, ok := node.(*ast.CaseClause)
	if ok && clause.List != nil {
		return 1
	}
	return 0
}

// commclausecomplexity handles comm clause complexity
func commClauseComplexity(node ast.Node) int {
	clause, ok := node.(*ast.CommClause)
	if ok && clause.Comm != nil {
		return 1
	}
	return 0
}

// binaryclausecomplexity handles binary clause complexity
func binaryClauseComplexity(node ast.Node) int {
	expression, ok := node.(*ast.BinaryExpr)
	if ok && (expression.Op == token.LAND || expression.Op == token.LOR) {
		return 1
	}
	return 0
}

// visitchildren handles visit children
func visitChildren(node ast.Node, depth int, visit func(ast.Node, int)) {
	ast.Inspect(node, func(child ast.Node) bool {
		if child == node {
			return true
		}
		if child != nil {
			visit(child, depth)
		}
		return false
	})
}

// tokenspan represents token span data
type tokenSpan struct{ start, end int }

// scantokens handles scan tokens
func scanTokens(src []byte, excluded []tokenSpan) []string {
	fset := token.NewFileSet()
	file := fset.AddFile("fragment.go", -1, len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, 0)
	result := make([]string, 0, len(src)/5)
	spanIndex := 0
	inExcluded := false
	for {
		value, done, include := nextScannedToken(&s, file, excluded, &spanIndex, &inExcluded)
		if done {
			break
		}
		if include {
			result = append(result, value)
		}
	}
	return result
}

// nextscannedtoken handles next scanned token
func nextScannedToken(s *scanner.Scanner, file *token.File, excluded []tokenSpan, spanIndex *int, inExcluded *bool) (string, bool, bool) {
	position, tok, literal := s.Scan()
	if tok == token.EOF {
		return "", true, false
	}
	if tok == token.SEMICOLON {
		return "", false, false
	}
	offset := file.Offset(position)
	advanceExcludedSpans(offset, excluded, spanIndex, inExcluded)
	if isExcludedOffset(offset, excluded, *spanIndex) {
		return excludedToken(inExcluded)
	}
	return formatScannedToken(tok, literal)
}

func excludedToken(inExcluded *bool) (string, bool, bool) {
	if *inExcluded {
		return "", false, false
	}
	*inExcluded = true
	return "<nested-function-body>", false, true
}

func formatScannedToken(tok token.Token, literal string) (string, bool, bool) {
	if tok.IsLiteral() {
		return tok.String() + ":" + literal, false, true
	}
	return tok.String(), false, true
}

// advanceexcludedspans handles advance excluded spans
func advanceExcludedSpans(offset int, excluded []tokenSpan, spanIndex *int, inExcluded *bool) {
	for *spanIndex < len(excluded) && offset >= excluded[*spanIndex].end {
		*spanIndex++
		*inExcluded = false
	}
}

// isexcludedoffset reports whether is excluded offset
func isExcludedOffset(offset int, excluded []tokenSpan, spanIndex int) bool {
	return spanIndex < len(excluded) && offset >= excluded[spanIndex].start && offset < excluded[spanIndex].end
}

// locatemodule handles locate module
func locateModule(root string) (string, string) {
	at := moduleSearchRoot(root)
	for {
		if name := moduleName(at); name != "" {
			return name, at
		}
		parent := filepath.Dir(at)
		if parent == at {
			return "", ""
		}
		at = parent
	}
}

// modulesearchroot handles module search root
func moduleSearchRoot(root string) string {
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		return filepath.Dir(root)
	}
	return root
}

// modulename handles module name
func moduleName(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if name, ok := parseModuleLine(line); ok {
			return name
		}
	}
	return ""
}

// parsemoduleline handles parse module line
func parseModuleLine(line string) (string, bool) {
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) < 2 || parts[0] != "module" {
		return "", false
	}
	name := strings.Trim(parts[1], `"`)
	return name, name != ""
}

// finalizereport handles finalize report
func finalizeReport(report *Report) {
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "go.mod not found") {
			continue
		}
		report.AnalysisStatus = "partial"
		report.CodeHealthScore = nil
		report.Classification = "unavailable"
		return
	}
	if report.CodeHealthScore != nil {
		report.Classification = classification(*report.CodeHealthScore)
	}
}
