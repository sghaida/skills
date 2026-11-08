package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
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
	parsed, err := parser.ParseFile(fset, path, src, parser.ParseComments)
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
	collectHealthMetrics(parsed, f)

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

func analyze(path string, includeTests bool) (Report, error) {
	return analyzeWithOptions(path, includeTests, false)
}

func analyzeWithOptions(path string, includeTests, includeGitChurn bool, workers ...int) (Report, error) {
	workerCount := 10
	if len(workers) > 0 {
		workerCount = workers[0]
	}
	return analyzeWithOptionsFiltered(path, includeTests, includeGitChurn, workerCount, nil)
}

func filterSourceFiles(paths []string, root string, fileFilter map[string]bool) ([]string, error) {
	if fileFilter == nil {
		return paths, nil
	}
	filtered := paths[:0]
	for _, candidate := range paths {
		relative, relativeErr := filepath.Rel(root, candidate)
		if relativeErr == nil && fileFilter[filepath.ToSlash(relative)] {
			filtered = append(filtered, candidate)
		}
	}
	return filtered, nil
}

func validateSourceFiles(paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("no eligible Go files found")
	}
	return nil
}

func postProcessReport(report *Report, parsedFiles []*sourceFile, includeGitChurn bool, hasModule bool) error {
	report.Files, report.Packages, report.Dimensions, report.CodeHealthScore = buildScores(parsedFiles, hasModule)
	finalizeReport(report)
	report.Hotspots = topHotspots(report.Files)
	if includeGitChurn {
		report.GitHotspots = topGitHotspots(report.Files)
	}
	return nil
}

func analyzeWithOptionsFiltered(path string, includeTests, includeGitChurn bool, workers int, fileFilter map[string]bool) (Report, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return Report{}, err
	}
	summary := Summary{}
	paths, err := sourceFiles(root, includeTests, &summary)
	if err != nil {
		return Report{}, err
	}
	if err := validateSourceFiles(paths); err != nil {
		return Report{}, fmt.Errorf("%w in %q", err, root)
	}
	duplicationEngine, err := loadDuplicationEngine(root)
	if err != nil {
		return Report{}, err
	}
	module, _ := locateModule(root)
	report := newReport(root, includeTests, includeGitChurn, duplicationEngine)

	paths, err = filterSourceFiles(paths, root, fileFilter)
	if err != nil {
		return Report{}, err
	}

	parsedFiles, err := parseSourceFiles(paths, root, &summary, &report, workers)
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
	collectPackageHealth(parsedFiles, module, &summary)
	summary.Health.DocumentationPercent = percent(summary.Health.DocumentedExportedSymbols, summary.Health.ExportedSymbols)
	if includeGitChurn {
		if err := applyGitChurn(root, parsedFiles, &summary, &report); err != nil {
			return Report{}, err
		}
	}
	if err := detectProjectDuplication(root, parsedFiles, duplicationEngine, &summary); err != nil {
		return Report{}, err
	}
	finalizeDuplicationSummary(parsedFiles, duplicationEngine, &summary)
	report.Summary = summary
	if err := postProcessReport(&report, parsedFiles, includeGitChurn, module != ""); err != nil {
		return Report{}, err
	}
	return report, nil
}

func collectPackageHealth(files []*sourceFile, module string, summary *Summary) {
	graph := analyzePackageGraph(files, module)
	for _, file := range files {
		value := graph[file.packagePath]
		file.packageFanIn = value.incoming
		file.packageDependencyDepth = value.depth
		if value.cycle {
			file.packageImportCycles = 1
		}
		summary.Health.ExportedSymbols += file.exportedSymbols
		summary.Health.DocumentedExportedSymbols += file.documentedExportedSymbols
		summary.Health.IgnoredReturnValues += file.ignoredReturnValues
		summary.Health.Panics += file.panics
		summary.Health.TODOs += file.todos
		summary.Health.FIXMEs += file.fixmes
		summary.Health.Goroutines += file.goroutines
		summary.Health.ChannelOperations += file.channelOperations
		summary.Health.ImportCycles += file.packageImportCycles
	}
}

func detectProjectDuplication(root string, files []*sourceFile, engine string, summary *Summary) error {
	if engine == duplicationEngineJSCPDRS {
		return detectWithJSCPDRS(root, files, summary)
	}
	detectDuplicates(files, 35)
	return nil
}

func finalizeDuplicationSummary(files []*sourceFile, engine string, summary *Summary) {
	if engine != duplicationEngineInternal {
		return
	}
	for _, file := range files {
		summary.DuplicateTokens += file.duplicateTokens
		summary.FunctionTokens += file.totalFunctionTokens
	}
	summary.DuplicationPercent = percent(summary.DuplicateTokens, summary.FunctionTokens)
}

func newReport(root string, includeTests, includeGitChurn bool, duplicationEngine string) Report {
	return Report{
		SchemaVersion: "1.0", ScoringVersion: scoringVersion, Language: "go", MaxScore: 10,
		Configuration: Configuration{IncludeTests: includeTests, IncludeGitChurn: includeGitChurn, DuplicateWindowTokens: 35, DuplicationEngine: duplicationEngine},
		Repository:    root, AnalysisStatus: "complete", Packages: []PackageReport{}, Files: []FileReport{},
		Hotspots: []FileReport{}, Warnings: []string{},
	}
}

func applyGitChurn(root string, files []*sourceFile, summary *Summary, report *Report) error {
	output, err := exec.Command("git", "-C", root, "log", "--format=", "--numstat", "--all", "--", ".").Output()
	if err != nil {
		return fmt.Errorf("read Git history: %w", err)
	}
	byPath := map[string]int{}
	for _, line := range strings.Split(string(output), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 || parts[0] == "-" || parts[1] == "-" {
			continue
		}
		added, addErr := strconv.Atoi(parts[0])
		deleted, deleteErr := strconv.Atoi(parts[1])
		if addErr != nil || deleteErr != nil {
			continue
		}
		byPath[filepath.ToSlash(parts[2])] += added + deleted
	}
	for _, file := range files {
		file.gitChurn = byPath[file.path]
		summary.Health.GitChurn += file.gitChurn
	}
	return nil
}

func topGitHotspots(files []FileReport) []FileReport {
	result := append([]FileReport(nil), files...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Health.GitChurn == result[j].Health.GitChurn {
			return result[i].Path < result[j].Path
		}
		return result[i].Health.GitChurn > result[j].Health.GitChurn
	})
	if len(result) > 5 {
		result = result[:5]
	}
	return result
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
