// Command go-code-health measures structural maintainability of Go source code.
// It only reads source files; it never builds or executes the target repository.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const scoringVersion = "go-v0.3.0"

// dimensions contains the analyzer's four maintainability dimension scores
type Dimensions struct {
	Complexity          *float64 `json:"complexity"`
	Structure           *float64 `json:"structure"`
	Duplication         *float64 `json:"duplication"`
	Coupling            *float64 `json:"coupling"`
	CyclicDependencies  *float64 `json:"cyclic_dependencies"`
	InterfaceCompliance *float64 `json:"interface_compliance"`
	DuckTyping          *float64 `json:"duck_typing"`
}

// healthmetrics contains measured health indicators for analyzed go files
type HealthMetrics struct {
	ExportedSymbols           int     `json:"exported_symbols"`
	DocumentedExportedSymbols int     `json:"documented_exported_symbols"`
	DocumentationPercent      float64 `json:"documentation_percent"`
	IgnoredReturnValues       int     `json:"ignored_return_values"`
	Panics                    int     `json:"panics"`
	TODOs                     int     `json:"todos"`
	FIXMEs                    int     `json:"fixmes"`
	Goroutines                int     `json:"goroutines"`
	ChannelOperations         int     `json:"channel_operations"`
	ImportCycles              int     `json:"import_cycles"`
	DependencyDepth           int     `json:"dependency_depth"`
	IncomingDependencies      int     `json:"incoming_dependencies"`
	GitChurn                  int     `json:"git_churn,omitempty"`
	InterfaceCount            int     `json:"interface_count"`
	UnusedInterfaces          int     `json:"unused_interfaces"`
	BroadInterfaces           int     `json:"broad_interfaces"`
	ImplicitImplementations   int     `json:"implicit_implementations"`
}

// finding describes a measured analyzer finding
type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
}

// functionreport contains measured metrics for one go function
type FunctionReport struct {
	Name                string `json:"name"`
	Line                int    `json:"line"`
	Lines               int    `json:"lines"`
	BranchingComplexity int    `json:"branching_complexity"`
	MaxNesting          int    `json:"max_nesting"`
}

// filereport contains measured metrics and findings for one go source file
type FileReport struct {
	Path                string           `json:"path"`
	Package             string           `json:"package"`
	Score               float64          `json:"code_health_score"`
	Classification      string           `json:"classification"`
	Lines               int              `json:"lines"`
	Functions           []FunctionReport `json:"functions"`
	InternalImports     int              `json:"internal_imports"`
	DuplicateTokens     int              `json:"duplicate_tokens"`
	TotalFunctionTokens int              `json:"total_function_tokens"`
	DuplicationPercent  float64          `json:"duplication_percent"`
	Dimensions          Dimensions       `json:"dimensions"`
	Health              HealthMetrics    `json:"health"`
	Findings            []Finding        `json:"findings"`
}

// packagereport contains aggregated metrics for one go package
type PackageReport struct {
	Path                 string     `json:"path"`
	Score                float64    `json:"code_health_score"`
	Files                int        `json:"files"`
	Lines                int        `json:"lines"`
	InternalDependencies int        `json:"internal_dependencies"`
	IncomingDependencies int        `json:"incoming_dependencies"`
	DependencyDepth      int        `json:"dependency_depth"`
	Dimensions           Dimensions `json:"dimensions"`
}

// summary contains aggregate metrics for the analyzed repository
type Summary struct {
	GoFiles                int           `json:"go_files"`
	SourceLines            int           `json:"source_lines"`
	Functions              int           `json:"functions"`
	DuplicateTokens        int           `json:"duplicate_tokens"`
	FunctionTokens         int           `json:"function_tokens"`
	DuplicationPercent     float64       `json:"duplication_percent"`
	ExcludedTestFiles      int           `json:"excluded_test_files"`
	ExcludedGeneratedFiles int           `json:"excluded_generated_files"`
	Health                 HealthMetrics `json:"health"`
}

// configuration records the analyzer options used for a report
type Configuration struct {
	IncludeTests            bool   `json:"include_tests"`
	IncludeGitChurn         bool   `json:"include_git_churn"`
	DuplicateWindowTokens   int    `json:"duplicate_window_tokens"`
	DuplicationEngine       string `json:"duplication_engine"`
	BroadInterfaceThreshold int    `json:"broad_interface_threshold"`
}

// report is the complete json-serializable analyzer result
type Report struct {
	SchemaVersion   string          `json:"schema_version"`
	ScoringVersion  string          `json:"scoring_version"`
	Language        string          `json:"language"`
	MaxScore        int             `json:"max_score"`
	Configuration   Configuration   `json:"configuration"`
	Repository      string          `json:"repository"`
	CodeHealthScore *float64        `json:"code_health_score"`
	Classification  string          `json:"classification"`
	AnalysisStatus  string          `json:"analysis_status"`
	Dimensions      Dimensions      `json:"dimensions"`
	Summary         Summary         `json:"summary"`
	Packages        []PackageReport `json:"packages"`
	Files           []FileReport    `json:"files"`
	Hotspots        []FileReport    `json:"hotspots"`
	GitHotspots     []FileReport    `json:"git_hotspots,omitempty"`
	Warnings        []string        `json:"warnings"`
}

// classification handles classification
func classification(score float64) string {
	switch {
	case score >= 9:
		return "excellent"
	case score >= 8:
		return "healthy"
	case score >= 6:
		return "moderate"
	case score >= 4:
		return "unhealthy"
	default:
		return "critical"
	}
}

// writetext handles write text
func writeText(w io.Writer, report Report) {
	writeTextHeader(w, report)
	writeTextWarnings(w, report)
	writeTextHotspots(w, report)
}

// writetextheader handles write text header
func writeTextHeader(w io.Writer, report Report) {
	if report.CodeHealthScore == nil {
		_, _ = fmt.Fprintf(w, "Code Health: unavailable (%s)\n", report.AnalysisStatus)
	} else {
		_, _ = fmt.Fprintf(w, "Go Code Health: %.1f/10 (%s)\n", *report.CodeHealthScore, report.Classification)
	}
	_, _ = fmt.Fprintf(w, "Analyzed: %d files, %d functions, %d source lines | Score model: %s\n", report.Summary.GoFiles, report.Summary.Functions, report.Summary.SourceLines, report.ScoringVersion)
	_, _ = fmt.Fprintf(w, "Dimensions: Complexity %s | Structure %s | Duplication %s | Coupling %s | Cycles %s | Interfaces %s | Duck typing %s\n",
		scoreLabel(report.Dimensions.Complexity), scoreLabel(report.Dimensions.Structure), scoreLabel(report.Dimensions.Duplication), scoreLabel(report.Dimensions.Coupling),
		scoreLabel(report.Dimensions.CyclicDependencies), scoreLabel(report.Dimensions.InterfaceCompliance), scoreLabel(report.Dimensions.DuckTyping))
	_, _ = fmt.Fprintf(w, "Exact duplicate function tokens: %.1f%%\n", report.Summary.DuplicationPercent)
}

// scorelabel handles score label
func scoreLabel(value *float64) string {
	if value == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.1f", *value)
}

// writetextwarnings handles write text warnings
func writeTextWarnings(w io.Writer, report Report) {
	for _, warning := range report.Warnings {
		_, _ = fmt.Fprintf(w, "Warning: %s\n", warning)
	}
}

// writefinding handles write finding
func writeFinding(w io.Writer, finding Finding) {
	_, _ = fmt.Fprintf(w, "   - %s: %s\n", finding.Rule, finding.Message)
}

// writefilehotspot handles write file hotspot
func writeFileHotspot(w io.Writer, index int, file FileReport) {
	_, _ = fmt.Fprintf(w, "%d. %.1f/10 %s\n", index+1, file.Score, file.Path)
	count := len(file.Findings)
	if count > 3 {
		count = 3
	}
	for j := 0; j < count; j++ {
		writeFinding(w, file.Findings[j])
	}
}

// writetexthotspots handles write text hotspots
func writeTextHotspots(w io.Writer, report Report) {
	if len(report.Hotspots) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nFiles needing attention:")
	for i, f := range report.Hotspots {
		writeFileHotspot(w, i, f)
	}
}

// main runs the command-line analyzer
func main() {
	os.Exit(run())
}

// runconfig contains command-line options for one analyzer run
type RunConfig struct {
	path            string
	output          string
	includeTests    bool
	includeGitChurn bool
	failUnder       float64
	workers         int
	noCache         bool
	cacheInfo       bool
	cacheDebug      bool
	cacheFile       string
	cacheFileSet    bool
	explainHotspots bool
	hotspotsScope   string
}

// parseflags handles parse flags
func parseFlags() RunConfig {
	path := flag.String("path", ".", "Go repository or directory to scan (read-only)")
	output := flag.String("format", "json", "Output format: json or text")
	includeTests := flag.Bool("include-tests", false, "Include *_test.go files")
	includeGitChurn := flag.Bool("include-git-churn", false, "Include changed-line counts from Git history")
	failUnder := flag.Float64("fail-under", -1, "Exit 3 if the score falls below the supplied 0-10 threshold")
	workers := flag.Int("workers", 10, "Number of parallel workers (1-256)")
	noCache := flag.Bool("no-cache", false, "Ignore cache, force full scan")
	cacheInfo := flag.Bool("cache-info", false, "Show cache status without scanning")
	cacheDebug := flag.Bool("cache-debug", false, "Print cache decision diagnostics to stderr")
	cacheFile := flag.String("cache-file", defaultCacheFileName, "Cache file location")
	explainHotspots := flag.Bool("explain-hotspots", false, "Generate detailed explanations for hotspots")
	hotspotsScope := flag.String("hotspots-scope", "file", "Hotspots scope: file or package")
	flag.Parse()
	cacheFileSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "cache-file" {
			cacheFileSet = true
		}
	})

	return RunConfig{
		path:            *path,
		output:          *output,
		includeTests:    *includeTests,
		includeGitChurn: *includeGitChurn,
		failUnder:       *failUnder,
		workers:         *workers,
		noCache:         *noCache,
		cacheInfo:       *cacheInfo,
		cacheDebug:      *cacheDebug,
		cacheFile:       *cacheFile,
		cacheFileSet:    cacheFileSet,
		explainHotspots: *explainHotspots,
		hotspotsScope:   *hotspotsScope,
	}
}

// cachePathFor resolves the default cache file against the analyzed project
// root; an explicit or absolute --cache-file value is used as provided.
func cachePathFor(cfg RunConfig, reportRoot string) string {
	if cfg.cacheFileSet || filepath.IsAbs(cfg.cacheFile) {
		return cfg.cacheFile
	}
	return filepath.Join(reportRoot, cfg.cacheFile)
}

// handleCacheInfo prints stored cache metadata and whether the cache would
// serve the current scan exactly.
func handleCacheInfo(cacheMgr *CacheManager, scan ScanContext) int {
	cache, err := cacheMgr.ReadCache()
	if err != nil || cache == nil {
		fmt.Fprintf(os.Stderr, "No valid cache found\n")
		return 0
	}
	fmt.Fprintf(os.Stderr, "Cache format: %s\nScoring version: %s\nAnalysis revision: %s\nCache from: %s\nScan duration: %dms\nFiles in manifest: %d\n",
		cache.FormatVersion, cache.ScoringVersion, cache.AnalysisRevision,
		cache.ScanTimestamp, cache.ScanDurationMs, len(cache.FileManifest))
	printCacheValidity(cache, scan)
	return 0
}

// printCacheValidity compares the stored fingerprints to the current scan.
func printCacheValidity(cache *CacheFile, scan ScanContext) {
	manifest, err := buildSourceManifest(scan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Current manifest unavailable: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "Context match: %t\nManifest match: %t\n",
		cache.ContextHash == scan.Fingerprint(),
		cache.ManifestHash == manifestFingerprint(manifest))
}

// cachedReport returns the cached complete report only on an exact semantic
// fingerprint and file manifest match.
func cachedReport(cacheMgr *CacheManager, contextHash, manifestHash string, noCache bool) (Report, bool) {
	if noCache {
		return Report{}, false
	}
	cache := cacheMgr.ValidCache(contextHash, manifestHash)
	if cache == nil {
		return Report{}, false
	}
	return *cache.Report, true
}

// generateexplanations handles generate explanations
func generateExplanations(report *Report, cfg RunConfig) {
	if !cfg.explainHotspots {
		return
	}
	explainer := NewHotspotsExplainer(report, cfg.hotspotsScope)
	explanations, err := explainer.GenerateExplanations()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to generate hotspot explanations: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "\n%s\n", explanations)
}

// checkfailunder handles check fail under
func checkFailUnder(report Report, failUnder float64) int {
	if failUnder >= 0 && report.CodeHealthScore != nil && *report.CodeHealthScore < failUnder {
		return 3
	}
	return 0
}

// emitcompletedreport handles emit completed report
func emitCompletedReport(report Report, cfg RunConfig) int {
	generateExplanations(&report, cfg)
	if emitErr := emitReport(cfg.output, report); emitErr != nil {
		fmt.Fprintln(os.Stderr, emitErr)
		return 2
	}
	if report.AnalysisStatus != "complete" {
		return 2
	}
	return checkFailUnder(report, cfg.failUnder)
}

// performAnalysis runs the analysis pipeline, reusing compatible cached file
// snapshots, and refreshes the cache with the resulting report, snapshots,
// manifest, and semantic fingerprint.
func performAnalysis(cacheMgr *CacheManager, scan ScanContext, cfg RunConfig,
	manifest map[string]string, manifestHash string) (Report, error) {
	start := time.Now()
	previous := previousCache(cacheMgr, cfg)
	reportCacheDecision(cfg, decideCacheStatus(cacheMgr, previous, scan, manifest, cfg.noCache))
	report, snapshots, err := analyzeScanWithCache(scan, cfg.workers, previous, manifest)
	if err != nil {
		return Report{}, err
	}
	writeAnalysisCache(cacheMgr, scan, manifest, manifestHash, snapshots, &report, time.Since(start).Milliseconds())
	return report, nil
}

// previousCache loads the snapshot-compatible previous cache unless bypassed.
func previousCache(cacheMgr *CacheManager, cfg RunConfig) *CacheFile {
	if cfg.noCache {
		return nil
	}
	return cacheMgr.ReusableCache()
}

// writeAnalysisCache persists the completed report; failures only warn.
func writeAnalysisCache(cacheMgr *CacheManager, scan ScanContext, manifest map[string]string,
	manifestHash string, snapshots map[string]FileSnapshot, report *Report, durationMs int64) {
	cache := &CacheFile{
		ContextHash:    scan.Fingerprint(),
		ManifestHash:   manifestHash,
		FileManifest:   manifest,
		Snapshots:      snapshots,
		Report:         report,
		ScanDurationMs: durationMs,
	}
	if err := cacheMgr.WriteCache(cache); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write cache: %v\n", err)
	}
}

// run handles run
func run() int {
	cfg := parseFlags()

	if err := validateFlags(cfg.output, cfg.failUnder, cfg.workers, cfg.hotspotsScope); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	scan, err := resolveScanContext(cfg.path, cfg.includeTests, cfg.includeGitChurn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cacheMgr := NewCacheManager(cachePathFor(cfg, scan.ReportRoot))
	if cfg.includeGitChurn {
		scan.GitRefsHash = gitRefsFingerprint(scan.Root)
	}

	if cfg.cacheInfo {
		return handleCacheInfo(cacheMgr, scan)
	}

	manifest, err := buildSourceManifest(scan)
	if err != nil {
		fmt.Fprintln(os.Stderr, "manifest error:", err)
		return 2
	}
	manifestHash := manifestFingerprint(manifest)

	if report, found := cachedReport(cacheMgr, scan.Fingerprint(), manifestHash, cfg.noCache); found {
		reportCacheDecision(cfg, CacheDecision{
			Status: cacheStatusHit, Reason: "exact-context-and-manifest-match",
			FilesTotal: len(manifest), SnapshotsReused: len(manifest),
		})
		return emitCompletedReport(report, cfg)
	}

	report, err := performAnalysis(cacheMgr, scan, cfg, manifest, manifestHash)
	if err != nil {
		fmt.Fprintln(os.Stderr, "analysis error:", err)
		return 2
	}

	return emitCompletedReport(report, cfg)
}

// validateflags handles validate flags
func validateFlags(output string, failUnder float64, workers int, hotspotsScope string) error {
	validators := []func() error{
		func() error { return validateOutput(output) },
		func() error { return validateFailUnder(failUnder) },
		func() error { return validateWorkers(workers) },
		func() error { return validateHotspotsScope(hotspotsScope) },
	}
	for _, validate := range validators {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

// validateoutput handles validate output
func validateOutput(output string) error {
	if output != "json" && output != "text" {
		return errors.New("invalid --format: expected json or text")
	}
	return nil
}

// validatefailunder handles validate fail under
func validateFailUnder(failUnder float64) error {
	if failUnder > 10 || (failUnder < 0 && failUnder != -1) {
		return errors.New("invalid --fail-under: expected 0-10")
	}
	return nil
}

// validateworkers handles validate workers
func validateWorkers(workers int) error {
	if workers < 1 || workers > 256 {
		return errors.New("invalid --workers: expected 1-256")
	}
	return nil
}

// validatehotspotsscope handles validate hotspots scope
func validateHotspotsScope(scope string) error {
	if scope != "file" && scope != "package" {
		return errors.New("invalid --hotspots-scope: expected file or package")
	}
	return nil
}

// emitreport handles emit report
func emitReport(output string, report Report) error {
	if output == "text" {
		writeText(os.Stdout, report)
		return nil
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return fmt.Errorf("failed to write JSON: %w", err)
	}
	return nil
}

// tophotspots handles top hotspots
func topHotspots(files []FileReport) []FileReport {
	matches := make([]FileReport, 0)
	for _, f := range files {
		if f.Score < 8 {
			matches = append(matches, f)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return strings.Compare(matches[i].Path, matches[j].Path) < 0
		}
		return matches[i].Score < matches[j].Score
	})
	if len(matches) > 5 {
		matches = matches[:5]
	}
	return matches
}
