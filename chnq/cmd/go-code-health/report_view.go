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
		return Report{}, fmt.Errorf("requested %s %q has no eligible analyzed files", kind, path)
	}
	report.Selection = selection
	report.Files, report.Packages = files, packages
	report.Hotspots = files
	report.GitHotspots = nil
	return report, nil
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
	_, _ = fmt.Fprint(w, "Project dimensions:")
	values := namedDimensions(report.Dimensions)
	for _, name := range sortedKeys(values) {
		_, _ = fmt.Fprintf(w, " %s=%s", name, scoreLabel(values[name]))
	}
	_, _ = fmt.Fprintln(w)
	writeSelection(w, report.Selection)
	writePolicy(w, report.Policy)
	if cfg.failUnder >= 0 {
		check := compareThreshold("fail-under", report.CodeHealthScore, cfg.failUnder)
		_, _ = fmt.Fprintf(w, "CLI score gate: %s (%s >= %g)\n", check.Status, scoreLabel(check.Measured), check.Threshold)
	}
	writeTextWarnings(w, report)
	findings := compactFindings(report, cfg.policy)
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
		measured := "N/A"
		if check.Measured != nil {
			measured = fmt.Sprintf("%g", *check.Measured)
		}
		_, _ = fmt.Fprintf(w, "- %s: %s %s %g [%s]\n", check.Name, measured, check.Operator, check.Threshold, check.Status)
	}
}

// compactfinding associates a measured finding with its report-relative path
type compactFinding struct {
	path    string
	finding Finding
}

// compactfindings considers all files, including those above the legacy hotspot cutoff
func compactFindings(report Report, policy *checkPolicy) []compactFinding {
	files := append([]FileReport(nil), report.Files...)
	sort.Slice(files, func(i, j int) bool {
		if files[i].Score == files[j].Score {
			return files[i].Path < files[j].Path
		}
		return files[i].Score < files[j].Score
	})
	findings := make([]compactFinding, 0)
	for _, file := range files {
		for _, finding := range file.Findings {
			findings = append(findings, compactFinding{path: file.Path, finding: finding})
		}
		if policy != nil {
			if file.Score < *policy.MinimumScore {
				findings = append(findings, compactFinding{path: file.Path, finding: Finding{
					Rule: "score-investigation", Message: fmt.Sprintf("File score %.1f is below project target %g; investigate contribution", file.Score, *policy.MinimumScore),
				}})
			}
			values := namedDimensions(file.Dimensions)
			for _, name := range sortedKeys(policy.MinimumDimensions) {
				value := values[name]
				if value != nil && *value < *policy.MinimumDimensions[name] {
					findings = append(findings, compactFinding{path: file.Path, finding: Finding{
						Rule: "dimension-investigation", Message: fmt.Sprintf("%s %.1f is below project target %g; investigate contribution", name, *value, *policy.MinimumDimensions[name]),
					}})
				}
			}
		}
	}
	return findings
}
