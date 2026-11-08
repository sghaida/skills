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
	path                string
	packagePath         string
	packageName         string
	lines               int
	imports             []string
	funcs               []*functionData
	internalImports     int
	packageFanOut       int
	duplicateTokens     int
	totalFunctionTokens int
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
		nextDepth := depth
		switch n := node.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
			complexity += 1 + depth
			nextDepth++
		case *ast.CaseClause:
			if n.List != nil {
				complexity++
			}
		case *ast.CommClause:
			if n.Comm != nil {
				complexity++
			}
		case *ast.BinaryExpr:
			if n.Op == token.LAND || n.Op == token.LOR {
				complexity++
			}
		}
		if nextDepth > maxDepth {
			maxDepth = nextDepth
		}
		// Recurse exactly once into children; ast.Inspect is used as a visitor adaptor.
		ast.Inspect(node, func(child ast.Node) bool {
			if child == node {
				return true
			}
			if child != nil {
				visit(child, nextDepth)
			}
			return false
		})
	}
	for _, statement := range body.List {
		visit(statement, 0)
	}
	return complexity, maxDepth
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
		position, tok, literal := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.SEMICOLON {
			continue
		}
		offset := file.Offset(position)
		for spanIndex < len(excluded) && offset >= excluded[spanIndex].end {
			spanIndex++
			inExcluded = false
		}
		if spanIndex < len(excluded) && offset >= excluded[spanIndex].start && offset < excluded[spanIndex].end {
			if !inExcluded {
				result = append(result, "<nested-function-body>")
				inExcluded = true
			}
			continue
		}
		if tok.IsLiteral() {
			result = append(result, tok.String()+":"+literal)
		} else {
			result = append(result, tok.String())
		}
	}
	return result
}

func locateModule(root string) (string, string) {
	at := root
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		at = filepath.Dir(root)
	}
	for {
		data, err := os.ReadFile(filepath.Join(at, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "module ") {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					name := strings.Trim(parts[1], `"`)
					if name != "" {
						return name, at
					}
				}
			}
		}
		parent := filepath.Dir(at)
		if parent == at {
			return "", ""
		}
		at = parent
	}
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
		name := entry.Name()
		if entry.IsDir() {
			if path == root {
				return nil
			}
			// Do not traverse vendor, hidden directories, testdata, or third-party trees.
			if name == "vendor" || name == "testdata" || name == "third_party" || name == "node_modules" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(name, ".go") {
			return nil
		}
		if strings.HasSuffix(name, "_test.go") && !includeTests {
			sum.ExcludedTestFiles++
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	sort.Strings(paths)
	return paths, err
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
	module, _ := locateModule(root)
	report := Report{
		SchemaVersion:  "1.0",
		ScoringVersion: scoringVersion,
		Language:       "go",
		MaxScore:       10,
		Configuration: Configuration{
			IncludeTests:          includeTests,
			DuplicateWindowTokens: 35,
		},
		Repository:     root,
		AnalysisStatus: "complete",
		Packages:       []PackageReport{},
		Files:          []FileReport{},
		Hotspots:       []FileReport{},
		Warnings:       []string{},
	}
	parsedFiles := make([]*sourceFile, 0, len(paths))
	rootDir := root
	if info, e := os.Stat(root); e == nil && !info.IsDir() {
		rootDir = filepath.Dir(root)
	}
	for _, p := range paths {
		src, readErr := os.ReadFile(p)
		if readErr != nil {
			report.Warnings = append(report.Warnings, p+": "+readErr.Error())
			continue
		}
		header := src
		if len(header) > 2048 {
			header = header[:2048]
		}
		if generatedHeader.Match(header) {
			summary.ExcludedGeneratedFiles++
			continue
		}
		relativePath, relErr := filepath.Rel(rootDir, p)
		if relErr != nil {
			return Report{}, relErr
		}
		f, parseErr := scanGo(p, relativePath, src)
		if parseErr != nil {
			report.Warnings = append(report.Warnings, relativePath+": "+parseErr.Error())
			continue
		}
		parsedFiles = append(parsedFiles, f)
		summary.GoFiles++
		summary.SourceLines += f.lines
		summary.Functions += len(f.funcs)
	}
	if len(parsedFiles) == 0 {
		return Report{}, fmt.Errorf("no parsable non-generated Go files in %q; warnings: %s", root, strings.Join(report.Warnings, "; "))
	}
	if module == "" {
		report.Warnings = append(report.Warnings, "go.mod not found: coupling score is unavailable, and remaining weights are renormalized")
	}
	// Structural package fan-out is based on distinct imports of this module, not external dependencies.
	deps := map[string]map[string]bool{}
	for _, f := range parsedFiles {
		if deps[f.packagePath] == nil {
			deps[f.packagePath] = map[string]bool{}
		}
		for _, imp := range f.imports {
			if withinModule(imp, module) {
				deps[f.packagePath][imp] = true
				f.internalImports++
			}
		}
	}
	for _, f := range parsedFiles {
		f.packageFanOut = len(deps[f.packagePath])
	}
	detectDuplicates(parsedFiles, 35)
	for _, f := range parsedFiles {
		summary.DuplicateTokens += f.duplicateTokens
		summary.FunctionTokens += f.totalFunctionTokens
	}
	summary.DuplicationPercent = percent(summary.DuplicateTokens, summary.FunctionTokens)
	report.Summary = summary
	report.Files, report.Packages, report.Dimensions, report.CodeHealthScore = buildScores(parsedFiles, module != "")
	if len(report.Warnings) != 0 {
		for _, warning := range report.Warnings {
			if strings.Contains(warning, "go.mod not found") {
				continue
			}
			report.AnalysisStatus = "partial"
			report.CodeHealthScore = nil
			report.Classification = "unavailable"
			break
		}
	}
	if report.CodeHealthScore != nil {
		report.Classification = classification(*report.CodeHealthScore)
	}
	report.Hotspots = topHotspots(report.Files)
	return report, nil
}
