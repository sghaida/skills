package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// reportselection identifies a display-only scope within complete-project analysis
type ReportSelection struct {
	Kind            string     `json:"kind"`
	Path            string     `json:"path"`
	CodeHealthScore float64    `json:"code_health_score"`
	Classification  string     `json:"classification"`
	Dimensions      Dimensions `json:"dimensions"`
	Files           int        `json:"files"`
	Lines           int        `json:"lines"`
}

// selectreport filters details without changing project measurements or policy
func selectReport(report Report, file, pkg string) (Report, error) {
	if file == "" && pkg == "" {
		return report, nil
	}
	target := file
	kind := "file"
	if pkg != "" {
		target, kind = pkg, "package"
	}
	path, err := selectionPath(report.Repository, target)
	if err != nil {
		return Report{}, err
	}
	selection := &ReportSelection{Kind: kind, Path: path}
	files := make([]FileReport, 0)
	packages := make([]PackageReport, 0)
	for _, entry := range report.Files {
		if (kind == "file" && entry.Path == path) ||
			(kind == "package" && filepath.ToSlash(filepath.Dir(entry.Path)) == path) {
			files = append(files, entry)
			if kind == "file" {
				selection.CodeHealthScore = entry.Score
				selection.Classification = entry.Classification
				selection.Dimensions = entry.Dimensions
				selection.Files, selection.Lines = 1, entry.Lines
			}
		}
	}
	for _, entry := range report.Packages {
		if (kind == "package" && entry.Path == path) ||
			(kind == "file" && entry.Path == filepath.ToSlash(filepath.Dir(path))) {
			packages = append(packages, entry)
			if kind == "package" {
				selection.CodeHealthScore = entry.Score
				selection.Classification = classification(entry.Score)
				selection.Dimensions = entry.Dimensions
				selection.Files, selection.Lines = entry.Files, entry.Lines
			}
		}
	}
	if len(files) == 0 || (kind == "package" && len(packages) == 0) {
		return Report{}, selectionError(kind, path, report.Warnings)
	}
	report.Selection = selection
	report.Files, report.Packages = files, packages
	report.Hotspots = files
	report.GitHotspots = nil
	return report, nil
}

// selectionerror preserves analysis diagnostics when a focused report cannot be produced
func selectionError(kind, path string, warnings []string) error {
	message := fmt.Sprintf("requested %s %q has no eligible analyzed files", kind, path)
	if len(warnings) > 0 {
		message += "; analysis warnings: " + strings.Join(warnings, "; ")
	}
	return errors.New(message)
}

// selectionpath resolves project-relative selectors and rejects escapes
func selectionPath(root, target string) (string, error) {
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	resolved, err := canonicalPath(target)
	if err != nil {
		return "", err
	}
	path, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", fmt.Errorf("resolve selection: %w", err)
	}
	if path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("selection must be inside project root %s", root)
	}
	return filepath.ToSlash(path), nil
}

// emitconfiguredreport writes compatible full output or a bounded compact view
func emitConfiguredReport(w io.Writer, cfg RunConfig, report Report) error {
	if cfg.output == "json" {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			return fmt.Errorf("failed to write JSON: %w", err)
		}
		return nil
	}
	var output bytes.Buffer
	if cfg.output == "compact" {
		writeCompact(&output, report, cfg)
	} else {
		writeText(&output, report)
		writeSelection(&output, report.Selection)
		if report.Policy != nil {
			writePolicy(&output, report.Policy)
		}
	}
	if _, err := w.Write(output.Bytes()); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return fmt.Errorf("failed to write report: %w", err)
	}
	return nil
}

// writecompact omits function inventories while retaining failures and warnings
func writeCompact(w io.Writer, report Report, cfg RunConfig) {
	_, _ = fmt.Fprintf(w, "Analysis: %s\nProject score: %s/10 (%s)\nModel: %s\n",
		report.AnalysisStatus, scoreLabel(report.CodeHealthScore), report.Classification, report.ScoringVersion)
	_, _ = fmt.Fprintf(w, "Project files: %d | Excluded tests: %d | Excluded generated: %d\n",
		report.Summary.GoFiles, report.Summary.ExcludedTestFiles, report.Summary.ExcludedGeneratedFiles)
	if cfg.verbose {
		_, _ = fmt.Fprint(w, "Project dimensions:")
		values := namedDimensions(report.Dimensions)
		for _, name := range sortedKeys(values) {
			_, _ = fmt.Fprintf(w, " %s=%s", name, scoreLabel(values[name]))
		}
		_, _ = fmt.Fprintln(w)
	}
	writeSelection(w, report.Selection)
	writeCompactPolicy(w, report.Policy, cfg.verbose)
	cliPassed := true
	if cfg.failUnder >= 0 {
		check := compareThreshold("fail-under", report.CodeHealthScore, cfg.failUnder)
		cliPassed = check.Status == "PASS"
		_, _ = fmt.Fprintf(w, "CLI score gate: %s (%s >= %g)\n", check.Status, scoreLabel(check.Measured), check.Threshold)
	}
	writeTextWarnings(w, report)
	if !cfg.verbose && report.Selection == nil && report.AnalysisStatus == "complete" &&
		report.CodeHealthScore != nil && report.Policy != nil && report.Policy.Status == "PASS" && cliPassed {
		return
	}
	findings := compactFindings(report)
	shown := min(len(findings), cfg.maxFindings)
	_, _ = fmt.Fprintf(w, "Findings: %d shown / %d available\n", shown, len(findings))
	for _, finding := range findings[:shown] {
		location := finding.path
		if finding.finding.Line > 0 {
			location += fmt.Sprintf(":%d", finding.finding.Line)
		}
		_, _ = fmt.Fprintf(w, "- %s [%s] %s\n", location, finding.finding.Rule, finding.finding.Message)
	}
}

// writeselection distinguishes focused scores from the project-wide gate
func writeSelection(w io.Writer, selection *ReportSelection) {
	if selection == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "Selected %s: %s | %.1f/10 (%s) | %d files, %d lines\n",
		selection.Kind, selection.Path, selection.CodeHealthScore, selection.Classification, selection.Files, selection.Lines)
	_, _ = fmt.Fprint(w, "Selected dimensions:")
	values := namedDimensions(selection.Dimensions)
	for _, name := range sortedKeys(values) {
		_, _ = fmt.Fprintf(w, " %s=%s", name, scoreLabel(values[name]))
	}
	_, _ = fmt.Fprintln(w, "\nContext: complete project; policy status applies to the project, not the selection")
}

// writepolicy emits every configured comparison, even when details are limited
func writePolicy(w io.Writer, policy *PolicyResult) {
	if policy == nil {
		_, _ = fmt.Fprintln(w, "Project policy: NOT CHECKED (use --check)")
		return
	}
	_, _ = fmt.Fprintf(w, "Project policy: %s\n", policy.Status)
	for _, check := range policy.Checks {
		writePolicyCheck(w, check)
	}
}

// writecompactpolicy summarizes passing checks without hiding violations
func writeCompactPolicy(w io.Writer, policy *PolicyResult, verbose bool) {
	if policy == nil {
		writePolicy(w, nil)
		return
	}
	_, _ = fmt.Fprintf(w, "Project policy: %s\n", policy.Status)
	passed := 0
	for _, check := range policy.Checks {
		if check.Status == "PASS" {
			passed++
		}
	}
	_, _ = fmt.Fprintf(w, "Checks: %d/%d passed\n", passed, len(policy.Checks))
	for _, check := range policy.Checks {
		if verbose || check.Status != "PASS" {
			writePolicyCheck(w, check)
		}
	}
}

// writepolicycheck formats a comparison consistently across presentation modes
func writePolicyCheck(w io.Writer, check PolicyCheck) {
	measured := "N/A"
	if check.Measured != nil {
		measured = fmt.Sprintf("%g", *check.Measured)
	}
	_, _ = fmt.Fprintf(w, "- %s: %s %s %g [%s]\n", check.Name, measured, check.Operator, check.Threshold, check.Status)
}

// compactfinding associates a measured finding with its report-relative path
type compactFinding struct {
	path      string
	finding   Finding
	score     float64
	relevance int
	synthetic bool
}

// compactfindings prioritizes measured gate evidence before fallback investigation hints
func compactFindings(report Report) []compactFinding {
	failed := make(map[string]PolicyCheck)
	if report.Policy != nil {
		for _, check := range report.Policy.Checks {
			if check.Status != "PASS" {
				failed[check.Name] = check
			}
		}
	}
	findings := make([]compactFinding, 0)
	for _, file := range report.Files {
		for _, finding := range file.Findings {
			findings = append(findings, compactFinding{
				path: file.Path, finding: finding, score: file.Score,
				relevance: findingRelevance(finding.Rule, failed),
			})
		}
		if hint := investigationHint(file, failed); hint != nil {
			findings = append(findings, *hint)
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findingLess(findings[i], findings[j]) })
	return findings
}

// findingless keeps measured evidence first and uses deterministic priority tie-breaks
func findingLess(a, b compactFinding) bool {
	if a.synthetic != b.synthetic {
		return !a.synthetic
	}
	if a.relevance != b.relevance {
		return a.relevance < b.relevance
	}
	if severityRank(a.finding.Severity) != severityRank(b.finding.Severity) {
		return severityRank(a.finding.Severity) < severityRank(b.finding.Severity)
	}
	if a.score != b.score {
		return a.score < b.score
	}
	if a.path != b.path {
		return a.path < b.path
	}
	if a.finding.Line != b.finding.Line {
		return a.finding.Line < b.finding.Line
	}
	if a.finding.Rule != b.finding.Rule {
		return a.finding.Rule < b.finding.Rule
	}
	return a.finding.Message < b.finding.Message
}

// severityrank orders the analyzer's severity levels ahead of unspecified values
func severityRank(severity string) int {
	switch severity {
	case "high":
		return 0
	case "warning":
		return 1
	case "info":
		return 2
	default:
		return 3
	}
}

// findingmetrics maps rule evidence to policy keys without asserting exact score attribution
func findingMetrics(rule string) []string {
	switch rule {
	case "high-complexity", "goto-usage", "undocumented-recursion":
		return []string{"minimum_dimensions.complexity", "minimum_score"}
	case "deep-nesting":
		return []string{"minimum_dimensions.complexity", "minimum_dimensions.structure", "minimum_score"}
	case "long-function", "large-file":
		return []string{"minimum_dimensions.structure", "minimum_score"}
	case "duplicate-blocks":
		return []string{"minimum_dimensions.duplication", "minimum_score"}
	case "high-coupling":
		return []string{"minimum_dimensions.coupling", "minimum_score"}
	case "import-cycle":
		return []string{"minimum_dimensions.cyclic_dependencies", "maximum_import_cycles"}
	case "interface-compliance", "duck-typing":
		return []string{"minimum_dimensions." + strings.ReplaceAll(rule, "-", "_")}
	case "undocumented-exported-symbols":
		return []string{"minimum_documentation_percent"}
	case "ignored-return-value":
		return []string{"maximum_ignored_return_values"}
	case "panic-usage":
		return []string{"maximum_panics"}
	case "high-git-churn":
		return []string{"maximum_git_churn"}
	default:
		return nil
	}
}

// findingrelevance puts failed dimensions ahead of other failed gates and unrelated findings
func findingRelevance(rule string, failed map[string]PolicyCheck) int {
	rank := 2
	for _, metric := range findingMetrics(rule) {
		if _, exists := failed[metric]; exists {
			if strings.HasPrefix(metric, "minimum_dimensions.") {
				return 0
			}
			rank = 1
		}
	}
	return rank
}

// filehasmetricevidence detects a measured rule that already explains a metric
func fileHasMetricEvidence(file FileReport, metric string) bool {
	for _, finding := range file.Findings {
		for _, name := range findingMetrics(finding.Rule) {
			if name == metric {
				return true
			}
		}
	}
	return false
}

// investigationhint supplies at most one uncovered failed-score hint per file
func investigationHint(file FileReport, failed map[string]PolicyCheck) *compactFinding {
	values := namedDimensions(file.Dimensions)
	var hint *compactFinding
	deficit := 0.0
	for _, name := range sortedKeys(failed) {
		check := failed[name]
		if !strings.HasPrefix(name, "minimum_dimensions.") || fileHasMetricEvidence(file, name) {
			continue
		}
		dimension := strings.TrimPrefix(name, "minimum_dimensions.")
		value := values[dimension]
		if value != nil && check.Threshold-*value > deficit {
			deficit = check.Threshold - *value
			hint = &compactFinding{path: file.Path, score: file.Score, synthetic: true, finding: Finding{
				Rule: "dimension-investigation", Severity: "info",
				Message: fmt.Sprintf("%s %.1f is below project target %g; investigate contribution", dimension, *value, check.Threshold),
			}}
		}
	}
	if hint == nil {
		if check, failed := failed["minimum_score"]; failed && file.Score < check.Threshold &&
			!fileHasMetricEvidence(file, "minimum_score") {
			hint = &compactFinding{path: file.Path, score: file.Score, relevance: 1, synthetic: true, finding: Finding{
				Rule: "score-investigation", Severity: "info",
				Message: fmt.Sprintf("File score %.1f is below project target %g; investigate contribution", file.Score, check.Threshold),
			}}
		}
	}
	return hint
}
