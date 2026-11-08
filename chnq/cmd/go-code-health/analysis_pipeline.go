package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// analyze handles analyze
func analyze(path string, includeTests bool) (Report, error) {
	return analyzeWithOptions(path, includeTests, false)
}

// analyzewithoptions handles analyze with options
func analyzeWithOptions(path string, includeTests, includeGitChurn bool, workers ...int) (Report, error) {
	workerCount := 10
	if len(workers) > 0 {
		workerCount = workers[0]
	}
	return analyzeWithOptionsFiltered(path, includeTests, includeGitChurn, workerCount, nil)
}

// filtersourcefiles handles filter source files
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

// validatesourcefiles handles validate source files
func validateSourceFiles(paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("no eligible Go files found")
	}
	return nil
}

// analysisinputs represents analysis inputs data
type analysisInputs struct {
	root              string
	summary           Summary
	paths             []string
	duplicationEngine string
	module            string
	report            Report
}

// prepareanalysis handles prepare analysis
func prepareAnalysis(path string, includeTests, includeGitChurn bool) (analysisInputs, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return analysisInputs{}, err
	}

	inputs := analysisInputs{root: root}
	inputs.paths, err = sourceFiles(root, includeTests, &inputs.summary)
	if err != nil {
		return analysisInputs{}, err
	}
	if err := validateSourceFiles(inputs.paths); err != nil {
		return analysisInputs{}, fmt.Errorf("%w in %q", err, root)
	}
	inputs.duplicationEngine, err = loadDuplicationEngine(root)
	if err != nil {
		return analysisInputs{}, err
	}
	inputs.module, _ = locateModule(root)
	inputs.report = newReport(root, includeTests, includeGitChurn, inputs.duplicationEngine)
	return inputs, nil
}

// filteranalysispaths handles filter analysis paths
func filterAnalysisPaths(inputs analysisInputs, fileFilter map[string]bool) (analysisInputs, error) {
	filtered, err := filterSourceFiles(inputs.paths, inputs.root, fileFilter)
	if err != nil {
		return analysisInputs{}, err
	}
	inputs.paths = filtered
	return inputs, nil
}

// analyzeparsedfiles handles analyze parsed files
func analyzeParsedFiles(inputs *analysisInputs, includeGitChurn bool, workers int) error {
	parsedFiles, err := parseSourceFiles(inputs.paths, inputs.root, &inputs.summary, &inputs.report, workers)
	if err != nil {
		return err
	}
	if len(parsedFiles) == 0 {
		return fmt.Errorf("no parsable non-generated Go files in %q; warnings: %s",
			inputs.root, strings.Join(inputs.report.Warnings, "; "))
	}
	if inputs.module == "" {
		inputs.report.Warnings = append(inputs.report.Warnings,
			"go.mod not found: coupling score is unavailable, and remaining weights are renormalized")
	}
	assignPackageDependencies(parsedFiles, inputs.module)
	collectPackageHealth(parsedFiles, inputs.module, &inputs.summary)
	inputs.summary.Health.DocumentationPercent = percent(
		inputs.summary.Health.DocumentedExportedSymbols,
		inputs.summary.Health.ExportedSymbols,
	)
	if includeGitChurn {
		if err := applyGitChurn(inputs.root, parsedFiles, &inputs.summary); err != nil {
			return err
		}
	}
	if err := completeDuplicationAnalysis(inputs, parsedFiles); err != nil {
		return err
	}
	finalizeDuplicationSummary(parsedFiles, inputs.duplicationEngine, &inputs.summary)
	inputs.report.Summary = inputs.summary
	return postProcessReport(&inputs.report, parsedFiles, includeGitChurn, inputs.module != "")
}

func completeDuplicationAnalysis(inputs *analysisInputs, parsedFiles []*sourceFile) error {
	return detectProjectDuplication(inputs.root, parsedFiles, inputs.duplicationEngine, &inputs.summary)
}

// postprocessreport handles post process report
func postProcessReport(report *Report, parsedFiles []*sourceFile, includeGitChurn bool, hasModule bool) error {
	report.Files, report.Packages, report.Dimensions, report.CodeHealthScore = buildScores(parsedFiles, hasModule)
	finalizeReport(report)
	report.Hotspots = topHotspots(report.Files)
	if includeGitChurn {
		report.GitHotspots = topGitHotspots(report.Files)
	}
	return nil
}

// analyzewithoptionsfiltered handles analyze with options filtered
func analyzeWithOptionsFiltered(path string, includeTests, includeGitChurn bool, workers int, fileFilter map[string]bool) (Report, error) {
	inputs, err := prepareAnalysis(path, includeTests, includeGitChurn)
	if err != nil {
		return Report{}, err
	}
	inputs, err = filterAnalysisPaths(inputs, fileFilter)
	if err != nil {
		return Report{}, err
	}
	if err := analyzeParsedFiles(&inputs, includeGitChurn, workers); err != nil {
		return Report{}, err
	}
	return inputs.report, nil
}

// collectpackagehealth handles collect package health
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

// detectprojectduplication handles detect project duplication
func detectProjectDuplication(root string, files []*sourceFile, engine string, summary *Summary) error {
	if engine == duplicationEngineJSCPDRS {
		return detectWithJSCPDRS(root, files, summary)
	}
	detectDuplicates(files, 35)
	return nil
}

// finalizeduplicationsummary handles finalize duplication summary
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

// newreport handles new report
func newReport(root string, includeTests, includeGitChurn bool, duplicationEngine string) Report {
	return Report{
		SchemaVersion: "1.0", ScoringVersion: scoringVersion, Language: "go", MaxScore: 10,
		Configuration: Configuration{IncludeTests: includeTests, IncludeGitChurn: includeGitChurn, DuplicateWindowTokens: 35, DuplicationEngine: duplicationEngine},
		Repository:    root, AnalysisStatus: "complete", Packages: []PackageReport{}, Files: []FileReport{},
		Hotspots: []FileReport{}, Warnings: []string{},
	}
}

// applygitchurn handles apply git churn
func applyGitChurn(root string, files []*sourceFile, summary *Summary) error {
	output, err := exec.CommandContext(context.Background(), "git", "-C", root, "log", "--format=", "--numstat", "--all", "--", ".").Output()
	if err != nil {
		return fmt.Errorf("read Git history: %w", err)
	}
	byPath := parseGitChurn(string(output))
	assignGitChurn(files, summary, byPath)
	return nil
}

// parsegitchurn handles parse git churn
func parseGitChurn(output string) map[string]int {
	byPath := map[string]int{}
	for _, line := range strings.Split(output, "\n") {
		path, churn, ok := parseGitChurnLine(line)
		if ok {
			byPath[path] += churn
		}
	}
	return byPath
}

func parseGitChurnLine(line string) (string, int, bool) {
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) != 3 || parts[0] == "-" || parts[1] == "-" {
		return "", 0, false
	}
	added, addErr := strconv.Atoi(parts[0])
	deleted, deleteErr := strconv.Atoi(parts[1])
	if addErr != nil || deleteErr != nil {
		return "", 0, false
	}
	return filepath.ToSlash(parts[2]), added + deleted, true
}

// assigngitchurn handles assign git churn
func assignGitChurn(files []*sourceFile, summary *Summary, byPath map[string]int) {
	for _, file := range files {
		file.gitChurn = byPath[file.path]
		summary.Health.GitChurn += file.gitChurn
	}
}

// topgithotspots handles top git hotspots
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
