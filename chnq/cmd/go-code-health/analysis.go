package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var generatedHeader = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

type functionData struct {
	report           FunctionReport
	tokens           []string
	duplicateMarkers []int
}

type sourceFile struct {
	path                       string
	packagePath                string
	packageName                string
	lines                      int
	imports                    []string
	funcs                      []*functionData
	internalImports            int
	packageFanOut              int
	duplicateTokens            int
	externalDuplicationPercent *float64
	externalDuplicateTokens    int
	externalTotalTokens        int
	totalFunctionTokens        int
}

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

func scanGo(path, relativePath string, src []byte) (*sourceFile, error) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, src, 0)
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
	for _, i := range parsed.Imports {
		importPath, e := strconv.Unquote(i.Path.Value)
		if e == nil {
			f.imports = append(f.imports, importPath)
		}
	}

	appendFunction := func(name string, body *ast.BlockStmt, from, to token.Pos) {
		if body == nil {
			return
		}
		line := fset.Position(from).Line
		endLine := fset.Position(to).Line
		complexity, depth := measureBody(body)
		startOffset := fset.Position(body.Pos()).Offset
		endOffset := fset.Position(body.End()).Offset
		// Exclude nested closure bodies from parent function token sequences.
		// Closures are analyzed independently and must not create artificial clones.
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
		tokens := scanTokens(src[startOffset:endOffset], excluded)
		f.funcs = append(f.funcs, &functionData{
			report: FunctionReport{
				Name:                name,
				Line:                line,
				Lines:               endLine - line + 1,
				BranchingComplexity: complexity,
				MaxNesting:          depth,
			},
			tokens: tokens,
		})
		f.totalFunctionTokens += len(tokens)
	}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil {
			name = "method:" + name
		}
		appendFunction(name, fn.Body, fn.Pos(), fn.End())
	}
	// A closure receives an independent score and is not double-counted in its parent.
	ast.Inspect(parsed, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			appendFunction(fmt.Sprintf("closure@%d", fset.Position(lit.Pos()).Line), lit.Body, lit.Pos(), lit.End())
		}
		return true
	})
	sort.Slice(f.funcs, func(i, j int) bool { return f.funcs[i].report.Line < f.funcs[j].report.Line })
	return f, nil
}

// This is a documented branching/nesting heuristic, not Sonar's cognitive complexity.
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

func nodeComplexity(node ast.Node, depth int) (int, int) {
	if increment, nested := controlFlowComplexity(node, depth); nested {
		return increment, depth + 1
	}
	return nodeClauseComplexity(node), depth
}

func controlFlowComplexity(node ast.Node, depth int) (int, bool) {
	switch node.(type) {
	case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
		return 1 + depth, true
	default:
		return 0, false
	}
}

func nodeClauseComplexity(node ast.Node) int {
	switch n := node.(type) {
	case *ast.CaseClause:
		if n.List != nil {
			return 1
		}
	case *ast.CommClause:
		if n.Comm != nil {
			return 1
		}
	case *ast.BinaryExpr:
		if n.Op == token.LAND || n.Op == token.LOR {
			return 1
		}
	}
	return 0
}

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

type tokenSpan struct{ start, end int }

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
		if !*inExcluded {
			*inExcluded = true
			return "<nested-function-body>", false, true
		}
		return "", false, false
	}
	if tok.IsLiteral() {
		return tok.String() + ":" + literal, false, true
	}
	return tok.String(), false, true
}

func advanceExcludedSpans(offset int, excluded []tokenSpan, spanIndex *int, inExcluded *bool) {
	for *spanIndex < len(excluded) && offset >= excluded[*spanIndex].end {
		*spanIndex = *spanIndex + 1
		*inExcluded = false
	}
}

func isExcludedOffset(offset int, excluded []tokenSpan, spanIndex int) bool {
	return spanIndex < len(excluded) && offset >= excluded[spanIndex].start && offset < excluded[spanIndex].end
}

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

func moduleSearchRoot(root string) string {
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		return filepath.Dir(root)
	}
	return root
}

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

func parseModuleLine(line string) (string, bool) {
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) < 2 || parts[0] != "module" {
		return "", false
	}
	name := strings.Trim(parts[1], `"`)
	return name, name != ""
}

func withinModule(imp, module string) bool { return module != "" && strings.HasPrefix(imp, module+"/") }

func sourceFiles(root string, includeTests bool, sum *Summary) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if !strings.HasSuffix(root, ".go") {
			return nil, errors.New("--path must be a directory or .go file")
		}
		return []string{root}, nil
	}
	paths := make([]string, 0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return directoryWalkAction(path, root, entry.Name())
		}
		if !isGoSource(entry) {
			return nil
		}
		if isExcludedTest(entry.Name(), includeTests) {
			if strings.HasSuffix(entry.Name(), "_test.go") {
				sum.ExcludedTestFiles++
			}
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

func directoryWalkAction(path, root, name string) error {
	if path == root {
		return nil
	}
	if name == "vendor" || name == "testdata" || name == "third_party" || name == "node_modules" || strings.HasPrefix(name, ".") {
		return filepath.SkipDir
	}
	return nil
}

func isGoSource(entry fs.DirEntry) bool {
	return entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".go")
}

func isExcludedTest(name string, includeTests bool) bool {
	return strings.HasSuffix(name, "_test.go") && !includeTests
}

func analyze(path string, includeTests bool) (Report, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return Report{}, err
	}
	summary := Summary{}
	paths, err := sourceFiles(root, includeTests, &summary)
	if err != nil {
		return Report{}, err
	}
	if len(paths) == 0 {
		return Report{}, fmt.Errorf("no eligible Go files found in %q", root)
	}
	duplicationEngine, err := loadDuplicationEngine(root)
	if err != nil {
		return Report{}, err
	}
	module, _ := locateModule(root)
	report := newReport(root, includeTests, duplicationEngine)
	parsedFiles, err := parseSourceFiles(paths, root, &summary, &report)
	if err != nil {
		return Report{}, err
	}
	if len(parsedFiles) == 0 {
		return Report{}, fmt.Errorf("no parsable non-generated Go files in %q; warnings: %s", root, strings.Join(report.Warnings, "; "))
	}
	if module == "" {
		report.Warnings = append(report.Warnings, "go.mod not found: coupling score is unavailable, and remaining weights are renormalized")
	}
	assignPackageDependencies(parsedFiles, module)
	if duplicationEngine == duplicationEngineJSCPDRS {
		if err := detectWithJSCPDRS(root, parsedFiles, &summary); err != nil {
			return Report{}, err
		}
	} else {
		detectDuplicates(parsedFiles, 35)
	}
	for _, f := range parsedFiles {
		if duplicationEngine == duplicationEngineInternal {
			summary.DuplicateTokens += f.duplicateTokens
			summary.FunctionTokens += f.totalFunctionTokens
		}
	}
	if duplicationEngine == duplicationEngineInternal {
		summary.DuplicationPercent = percent(summary.DuplicateTokens, summary.FunctionTokens)
	}
	report.Summary = summary
	report.Files, report.Packages, report.Dimensions, report.CodeHealthScore = buildScores(parsedFiles, module != "")
	finalizeReport(&report)
	report.Hotspots = topHotspots(report.Files)
	return report, nil
}

func newReport(root string, includeTests bool, duplicationEngine string) Report {
	return Report{
		SchemaVersion: "1.0", ScoringVersion: scoringVersion, Language: "go", MaxScore: 10,
		Configuration: Configuration{IncludeTests: includeTests, DuplicateWindowTokens: 35, DuplicationEngine: duplicationEngine},
		Repository:    root, AnalysisStatus: "complete", Packages: []PackageReport{}, Files: []FileReport{},
		Hotspots: []FileReport{}, Warnings: []string{},
	}
}

func parseSourceFiles(paths []string, root string, summary *Summary, report *Report) ([]*sourceFile, error) {
	rootDir := moduleSearchRoot(root)
	parsedFiles := make([]*sourceFile, 0, len(paths))
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			report.Warnings = append(report.Warnings, path+": "+err.Error())
			continue
		}
		if generatedHeader.Match(src[:minInt(len(src), 2048)]) {
			summary.ExcludedGeneratedFiles++
			continue
		}
		relativePath, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil, err
		}
		file, err := scanGo(path, relativePath, src)
		if err != nil {
			report.Warnings = append(report.Warnings, relativePath+": "+err.Error())
			continue
		}
		parsedFiles = append(parsedFiles, file)
		summary.GoFiles++
		summary.SourceLines += file.lines
		summary.Functions += len(file.funcs)
	}
	return parsedFiles, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func assignPackageDependencies(files []*sourceFile, module string) {
	deps := map[string]map[string]bool{}
	for _, file := range files {
		if deps[file.packagePath] == nil {
			deps[file.packagePath] = map[string]bool{}
		}
		for _, imp := range file.imports {
			if withinModule(imp, module) {
				deps[file.packagePath][imp] = true
				file.internalImports++
			}
		}
	}
	for _, file := range files {
		file.packageFanOut = len(deps[file.packagePath])
	}
}

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
