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
	scan, err := resolveScanContext(path, includeTests, includeGitChurn)
	if err != nil {
		return Report{}, err
	}
	report, _, err := analyzeScan(scan, workerCount)
	return report, err
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
	root                    string
	summary                 Summary
	paths                   []string
	duplicationEngine       string
	broadInterfaceThreshold int
	module                  string
	report                  Report
}

// analyzeScan runs the complete analysis pipeline for one resolved scan
// context and returns the report plus file-local snapshots for caching.
func analyzeScan(scan ScanContext, workers int) (Report, map[string]FileSnapshot, error) {
	return analyzeScanWithCache(scan, workers, nil, nil)
}

// analyzeScanWithCache runs the single analysis pipeline, restoring unchanged
// files from compatible cached snapshots instead of reparsing them. Every
// project-derived metric is still recomputed over the complete file model, so
// cold and incremental scans share one aggregation path.
func analyzeScanWithCache(scan ScanContext, workers int, previous *CacheFile,
	manifest map[string]string) (Report, map[string]FileSnapshot, error) {
	inputs, err := prepareAnalysisContext(scan)
	if err != nil {
		return Report{}, nil, err
	}
	restored := restoreUnchangedFiles(&inputs, scan, previous, manifest)
	parsed, err := parseSourceFiles(inputs.paths, inputs.root, &inputs.summary, &inputs.report, workers)
	if err != nil {
		return Report{}, nil, err
	}
	files := mergeModelFiles(parsed, restored)
	if err := analyzeProjectModel(&inputs, files, scan.IncludeGitChurn); err != nil {
		return Report{}, nil, err
	}
	return inputs.report, snapshotFiles(files), nil
}

// restoreUnchangedFiles removes snapshot-backed unchanged paths from the
// parse list and returns their restored file models, accumulating the same
// summary counters the parser would have produced for them.
func restoreUnchangedFiles(inputs *analysisInputs, scan ScanContext,
	previous *CacheFile, manifest map[string]string) []*sourceFile {
	reusable := reusableSnapshots(previous, manifest)
	if len(reusable) == 0 {
		return nil
	}
	parsePaths := make([]string, 0, len(inputs.paths))
	restored := make([]*sourceFile, 0, len(reusable))
	for _, path := range inputs.paths {
		snapshot, ok := reusable[reportRelativePath(scan.ReportRoot, path)]
		if !ok {
			parsePaths = append(parsePaths, path)
			continue
		}
		restored = append(restored, restoreSummarizedFile(&inputs.summary, snapshot))
	}
	inputs.paths = parsePaths
	return restored
}

// restoreSummarizedFile restores one snapshot and applies its parse counters.
func restoreSummarizedFile(summary *Summary, snapshot FileSnapshot) *sourceFile {
	file := restoreFile(snapshot)
	summary.GoFiles++
	summary.SourceLines += file.lines
	summary.Functions += len(file.funcs)
	return file
}

// reusableSnapshots selects cached snapshots whose file content is unchanged
// in the current manifest.
func reusableSnapshots(previous *CacheFile, manifest map[string]string) map[string]FileSnapshot {
	if previous == nil {
		return nil
	}
	reusable := make(map[string]FileSnapshot)
	for path, hash := range manifest {
		if previous.FileManifest[path] != hash {
			continue
		}
		if snapshot, ok := previous.Snapshots[path]; ok {
			reusable[path] = snapshot
		}
	}
	return reusable
}

// reportRelativePath converts an absolute source path to the slash-separated
// report-relative form used by manifests and snapshots.
func reportRelativePath(reportRoot, path string) string {
	relative, err := filepath.Rel(reportRoot, path)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(relative)
}

// mergeModelFiles combines parsed and restored files in the deterministic
// path order the cold pipeline produces.
func mergeModelFiles(parsed, restored []*sourceFile) []*sourceFile {
	files := make([]*sourceFile, 0, len(parsed)+len(restored))
	files = append(files, parsed...)
	files = append(files, restored...)
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files
}

// prepareAnalysisContext discovers eligible files and builds analysis inputs
// from one immutable scan context, so cache validation and analysis can never
// resolve options independently.
func prepareAnalysisContext(scan ScanContext) (analysisInputs, error) {
	inputs := analysisInputs{root: scan.Root}
	var err error
	inputs.paths, err = sourceFiles(scan.Root, scan.IncludeTests, &inputs.summary)
	if err != nil {
		return analysisInputs{}, err
	}
	if err := validateSourceFiles(inputs.paths); err != nil {
		return analysisInputs{}, fmt.Errorf("%w in %q", err, scan.Root)
	}
	inputs.duplicationEngine = scan.DuplicationEngine
	inputs.broadInterfaceThreshold = scan.BroadInterfaceThreshold
	inputs.module = scan.ModulePath
	inputs.report = newReport(scan)
	return inputs, nil
}

// analyzeProjectModel recomputes every project-derived metric from the
// complete file model and finalizes the report through the single
// aggregation path shared by cold and incremental scans.
func analyzeProjectModel(inputs *analysisInputs, files []*sourceFile, includeGitChurn bool) error {
	if len(files) == 0 {
		return fmt.Errorf("no parsable non-generated Go files in %q; warnings: %s",
			inputs.root, strings.Join(inputs.report.Warnings, "; "))
	}
	if inputs.module == "" {
		inputs.report.Warnings = append(inputs.report.Warnings,
			"go.mod not found: coupling score is unavailable, and remaining weights are renormalized")
	}
	assignPackageDependencies(files, inputs.module)
	collectPackageHealth(files, inputs.module, &inputs.summary)
	inputs.summary.Health.DocumentationPercent = percent(
		inputs.summary.Health.DocumentedExportedSymbols,
		inputs.summary.Health.ExportedSymbols,
	)
	if includeGitChurn {
		if err := applyGitChurn(inputs.root, files, &inputs.summary); err != nil {
			return err
		}
	}
	analyzeArchitecture(files, inputs.broadInterfaceThreshold)
	collectArchitectureHealth(files, &inputs.summary, inputs.broadInterfaceThreshold)
	if err := completeDuplicationAnalysis(inputs, files); err != nil {
		return err
	}
	finalizeDuplicationSummary(files, inputs.duplicationEngine, &inputs.summary)
	inputs.report.Summary = inputs.summary
	return postProcessReport(&inputs.report, files, includeGitChurn, inputs.module != "")
}

func completeDuplicationAnalysis(inputs *analysisInputs, parsedFiles []*sourceFile) error {
	return detectProjectDuplication(inputs.root, parsedFiles, inputs.duplicationEngine, &inputs.summary)
}

// postprocessreport handles post process report
func postProcessReport(report *Report, parsedFiles []*sourceFile, includeGitChurn bool, hasModule bool) error {
	policy := scoringPolicy{
		gotoCheck:        report.Configuration.GotoCheck,
		maxFunctionLines: report.Configuration.MaxFunctionLines,
		recursionCheck:   report.Configuration.RecursionCheck,
	}
	report.Files, report.Packages, report.Dimensions, report.CodeHealthScore = buildScores(parsedFiles, hasModule, policy)
	finalizeReport(report)
	report.Hotspots = topHotspots(report.Files)
	if includeGitChurn {
		report.GitHotspots = topGitHotspots(report.Files)
	}
	return nil
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
func newReport(scan ScanContext) Report {
	return Report{
		SchemaVersion: "1.0", ScoringVersion: scoringVersion, Language: "go", MaxScore: 10,
		Configuration: Configuration{
			IncludeTests: scan.IncludeTests, IncludeGitChurn: scan.IncludeGitChurn,
			DuplicateWindowTokens: 35, DuplicationEngine: scan.DuplicationEngine,
			BroadInterfaceThreshold: scan.BroadInterfaceThreshold,
			GotoCheck:               scan.GotoCheck, MaxFunctionLines: scan.MaxFunctionLines,
			RecursionCheck: scan.RecursionCheck,
		},
		Repository: scan.Root, AnalysisStatus: "complete", Packages: []PackageReport{}, Files: []FileReport{},
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
