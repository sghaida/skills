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
	"sort"
	"strings"
)

const scoringVersion = "go-v0.1.0"

type Dimensions struct {
	Complexity  *float64 `json:"complexity"`
	Structure   *float64 `json:"structure"`
	Duplication *float64 `json:"duplication"`
	Coupling    *float64 `json:"coupling"`
}

type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
}

type FunctionReport struct {
	Name                string `json:"name"`
	Line                int    `json:"line"`
	Lines               int    `json:"lines"`
	BranchingComplexity int    `json:"branching_complexity"`
	MaxNesting          int    `json:"max_nesting"`
}

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
	Findings            []Finding        `json:"findings"`
}

type PackageReport struct {
	Path                 string  `json:"path"`
	Score                float64 `json:"code_health_score"`
	Files                int     `json:"files"`
	Lines                int     `json:"lines"`
	InternalDependencies int     `json:"internal_dependencies"`
}

type Summary struct {
	GoFiles                int     `json:"go_files"`
	SourceLines            int     `json:"source_lines"`
	Functions              int     `json:"functions"`
	DuplicateTokens        int     `json:"duplicate_tokens"`
	FunctionTokens         int     `json:"function_tokens"`
	DuplicationPercent     float64 `json:"duplication_percent"`
	ExcludedTestFiles      int     `json:"excluded_test_files"`
	ExcludedGeneratedFiles int     `json:"excluded_generated_files"`
}

type Configuration struct {
	IncludeTests          bool   `json:"include_tests"`
	DuplicateWindowTokens int    `json:"duplicate_window_tokens"`
	DuplicationEngine     string `json:"duplication_engine"`
}

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
	Warnings        []string        `json:"warnings"`
}

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

func writeText(w io.Writer, report Report) {
	writeTextHeader(w, report)
	writeTextWarnings(w, report)
	writeTextHotspots(w, report)
}

func writeTextHeader(w io.Writer, report Report) {
	if report.CodeHealthScore == nil {
		fmt.Fprintf(w, "Code Health: unavailable (%s)\n", report.AnalysisStatus)
	} else {
		fmt.Fprintf(w, "Go Code Health: %.1f/10 (%s)\n", *report.CodeHealthScore, report.Classification)
	}
	fmt.Fprintf(w, "Analyzed: %d files, %d functions, %d source lines | Score model: %s\n", report.Summary.GoFiles, report.Summary.Functions, report.Summary.SourceLines, report.ScoringVersion)
	fmt.Fprintf(w, "Dimensions: Complexity %s | Structure %s | Duplication %s | Coupling %s\n",
		scoreLabel(report.Dimensions.Complexity), scoreLabel(report.Dimensions.Structure), scoreLabel(report.Dimensions.Duplication), scoreLabel(report.Dimensions.Coupling))
	fmt.Fprintf(w, "Exact duplicate function tokens: %.1f%%\n", report.Summary.DuplicationPercent)
}

func scoreLabel(value *float64) string {
	if value == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.1f", *value)
}

func writeTextWarnings(w io.Writer, report Report) {
	for _, warning := range report.Warnings {
		fmt.Fprintf(w, "Warning: %s\n", warning)
	}
}

func writeTextHotspots(w io.Writer, report Report) {
	if len(report.Hotspots) > 0 {
		fmt.Fprintln(w, "\nFiles needing attention:")
		for i, f := range report.Hotspots {
			fmt.Fprintf(w, "%d. %.1f/10 %s\n", i+1, f.Score, f.Path)
			for j, finding := range f.Findings {
				if j >= 3 {
					break
				}
				fmt.Fprintf(w, "   - %s: %s\n", finding.Rule, finding.Message)
			}
		}
	}
}

func main() {
	os.Exit(run())
}

func run() int {
	path := flag.String("path", ".", "Go repository or directory to scan (read-only)")
	output := flag.String("format", "json", "Output format: json or text")
	includeTests := flag.Bool("include-tests", false, "Include *_test.go files")
	failUnder := flag.Float64("fail-under", -1, "Exit 3 if the score falls below the supplied 0-10 threshold")
	flag.Parse()

	if err := validateFlags(*output, *failUnder); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	report, err := analyze(*path, *includeTests)
	if err != nil {
		fmt.Fprintln(os.Stderr, "analysis error:", err)
		return 2
	}
	if err := emitReport(*output, report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if report.AnalysisStatus != "complete" {
		return 2
	}
	if *failUnder >= 0 && report.CodeHealthScore != nil && *report.CodeHealthScore < *failUnder {
		return 3
	}
	return 0
}

func validateFlags(output string, failUnder float64) error {
	if output != "json" && output != "text" {
		return errors.New("invalid --format: expected json or text")
	}
	if failUnder > 10 || (failUnder < 0 && failUnder != -1) {
		return errors.New("invalid --fail-under: expected 0-10")
	}
	return nil
}

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
