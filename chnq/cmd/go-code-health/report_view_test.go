package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// runhealthcli captures a complete invocation without leaking global flag state
func runHealthCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "stdout-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	stderr, err := os.CreateTemp(t.TempDir(), "stderr-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stderr.Close() }()
	oldOut, oldErr, oldArgs, oldFlags := os.Stdout, os.Stderr, os.Args, flag.CommandLine
	defer func() {
		os.Stdout, os.Stderr, os.Args, flag.CommandLine = oldOut, oldErr, oldArgs, oldFlags
	}()
	os.Stdout, os.Stderr = out, stderr
	os.Args = append([]string{"go-code-health"}, args...)
	flag.CommandLine = flag.NewFlagSet("go-code-health", flag.ContinueOnError)
	flag.CommandLine.SetOutput(stderr)
	code := run()
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := stderr.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics, err := io.ReadAll(stderr)
	if err != nil {
		t.Fatal(err)
	}
	return code, string(output), string(diagnostics)
}

// testviewproject includes cross-package duplicates and an unrelated policy violation
func testViewProject(t *testing.T) string {
	t.Helper()
	root, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "go.mod", "module example.com/view\n")
	code := "func calculate(n int) int { result := n; for i:=0; i<n; i++ { if i%2==0 { result += i*n } else { result -= i+n } }; return result }\n"
	writeFixture(t, root, "lib/a.go", "package lib\n"+code)
	writeFixture(t, root, "other/b.go", "package other\n"+code)
	writeFixture(t, root, "lib/sub/bad.go", "package sub\nfunc bad() { panic(\"bad\") }\n")
	writeFixture(t, root, "lib/helper_test.go", "package lib\nfunc testHelper() {}\n")
	writeFixture(t, root, projectConfigFileName, `{"version":1,"duplication_engine":"internal","minimum_score":0,
		"minimum_dimensions":{"complexity":0},"health_thresholds":{"maximum_panics":0},"include_tests":true}`)
	return root
}

// testscopedclicontext verifies cache parity, exact package scope, and project-wide enforcement
func TestScopedCLIContext(t *testing.T) {
	root := testViewProject(t)
	code, output, diagnostics := runHealthCLI(t, "--path", root, "--check", "--format", "json", "--no-cache")
	if code != 3 {
		t.Fatalf("expected policy failure: %d %s", code, diagnostics)
	}
	var baseline Report
	if err := json.Unmarshal([]byte(output), &baseline); err != nil {
		t.Fatal(err)
	}
	if baseline.Summary.GoFiles != 4 || !baseline.Configuration.IncludeTests || baseline.Summary.DuplicateTokens == 0 {
		t.Fatalf("missing configured tests or cross-package duplication: %+v", baseline.Summary)
	}
	var expected FileReport
	for _, file := range baseline.Files {
		if file.Path == "lib/a.go" {
			expected = file
		}
	}
	for _, args := range [][]string{
		{"--file", "lib/a.go"}, {"--file", filepath.Join(root, "lib/a.go"), "--no-cache"},
		{"--package", "lib"}, {"--package", "lib", "--no-cache"},
	} {
		command := append([]string{"--path", root, "--check", "--format", "json"}, args...)
		code, output, diagnostics = runHealthCLI(t, command...)
		if code != 3 {
			t.Fatalf("selected healthy scope hid project failure: %d %s", code, diagnostics)
		}
		var selected Report
		if err := json.Unmarshal([]byte(output), &selected); err != nil {
			t.Fatal(err)
		}
		if selected.Selection == nil || !reflect.DeepEqual(selected.Summary, baseline.Summary) ||
			!reflect.DeepEqual(selected.Policy, baseline.Policy) || !reflect.DeepEqual(selected.Dimensions, baseline.Dimensions) {
			t.Fatalf("selection changed project context: %+v", selected)
		}
		if !reflect.DeepEqual(selected.Files[0], expected) {
			t.Fatal("selection changed measured file metrics")
		}
		if selected.Selection.Kind == "package" && (len(selected.Files) != 2 || len(selected.Packages) != 1) {
			t.Fatalf("package selection included descendants: %+v", selected.Selection)
		}
		if selected.Selection.Kind == "file" && len(selected.Files) != 1 {
			t.Fatal("file selection returned unrelated files")
		}
	}
	code, compact, diagnostics := runHealthCLI(t, "--path", root, "--check", "--format", "compact", "--file", "lib/a.go", "--max-findings", "0")
	if code != 3 || !strings.Contains(compact, "Project policy: FAIL") ||
		!strings.Contains(compact, "maximum_panics: 1 <= 0 [FAIL]") ||
		!strings.Contains(compact, "Selected file: lib/a.go") || strings.Contains(compact, "\n- lib/") {
		t.Fatalf("compact scope or failure list incorrect: %d\n%s\n%s", code, compact, diagnostics)
	}
	if len(compact)*2 >= len(output) {
		t.Fatalf("compact output did not halve JSON bytes: %d vs %d", len(compact), len(output))
	}
}

// testpolicycliexits distinguishes passing gates, analysis errors, and legacy scans
func TestPolicyCLIExits(t *testing.T) {
	root := testViewProject(t)
	writeFixture(t, root, projectConfigFileName, `{"version":1,"minimum_score":0,"health_thresholds":{"maximum_panics":1}}`)
	for _, format := range []string{"json", "text", "compact"} {
		if code, _, diagnostic := runHealthCLI(t, "--path", root, "--check", "--format", format); code != 0 {
			t.Fatalf("passing %s gate failed: %d %s", format, code, diagnostic)
		}
	}
	if code, _, _ := runHealthCLI(t, "--path", root, "--check", "--fail-under", "10", "--format", "compact"); code != 3 {
		t.Fatal("additional CLI threshold was ignored")
	}
	if code, output, _ := runHealthCLI(t, "--path", root, "--format", "compact"); code != 0 ||
		!strings.Contains(output, "Project policy: NOT CHECKED") {
		t.Fatal("legacy scan claimed policy enforcement")
	}
	if code, output, _ := runHealthCLI(t, "--path", root, "--format", "text"); code != 0 ||
		strings.Contains(output, "Project policy:") {
		t.Fatal("legacy text output changed")
	}
	for _, args := range [][]string{
		{"--file", "missing.go"}, {"--package", "missing"}, {"--file", "../outside.go"},
		{"--file", "lib/a.go", "--package", "lib"}, {"--max-findings", "-1"},
		{"--check", "--cache-info"}, {"--check", "--include-tests"},
	} {
		if code, _, diagnostic := runHealthCLI(t, append([]string{"--path", root}, args...)...); code != 2 || diagnostic == "" {
			t.Fatalf("invalid scope/options accepted: %v code=%d %s", args, code, diagnostic)
		}
	}
	writeFixture(t, root, "broken.go", "package broken\nfunc {\n")
	if code, output, _ := runHealthCLI(t, "--path", root, "--check", "--format", "compact"); code != 2 ||
		!strings.Contains(output, "Project policy: ERROR") || !strings.Contains(output, "Warning:") {
		t.Fatalf("partial analysis became a policy result: %d\n%s", code, output)
	}
}

// testcompactfindinglimits keeps all policy failures and sees files above eight
func TestCompactFindingLimits(t *testing.T) {
	policy := testCheckPolicy(t, t.TempDir())
	report := testGateReport()
	report.Dimensions.Complexity = ptr(8.5)
	report.Summary.Health.Panics = 1
	report.Policy = evaluatePolicy(report, policy)
	report.Files = []FileReport{
		{Path: "b.go", Score: 8.5, Dimensions: Dimensions{Complexity: ptr(8.5)}},
		{Path: "a.go", Score: 8.5, Findings: []Finding{{Rule: "long-function", Line: 12, Message: "too long"}}},
	}
	report.Hotspots = topHotspots(report.Files)
	if len(report.Hotspots) != 0 {
		t.Fatal("fixture should be above the legacy hotspot cutoff")
	}
	var output bytes.Buffer
	cfg := RunConfig{output: "compact", policy: policy, maxFindings: 1, failUnder: -1}
	if err := emitConfiguredReport(&output, cfg, report); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"minimum_dimensions.complexity: 8.5 >= 9 [FAIL]",
		"maximum_panics: 1 <= 0 [FAIL]", "Findings: 1 shown / 2 available", "- a.go:12 [long-function]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("compact output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\n- b.go") {
		t.Fatal("finding limit ignored")
	}
	var second bytes.Buffer
	if err := emitConfiguredReport(&second, cfg, report); err != nil || second.String() != text {
		t.Fatal("compact output is not deterministic")
	}

	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/limits\n")
	for i := range 12 {
		writeFixture(t, root, fmt.Sprintf("file%d.go", i),
			fmt.Sprintf("package limits\nfunc action%d() { panic(\"example\") }\n", i))
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "Findings: 10 shown / 12 available"},
		{[]string{"--max-findings", "3"}, "Findings: 3 shown / 12 available"},
	} {
		args := append([]string{"--path", root, "--format", "compact"}, tc.args...)
		code, output, diagnostics := runHealthCLI(t, args...)
		if code != 0 || !strings.Contains(output, tc.want) {
			t.Fatalf("CLI findings budget incorrect: code=%d\n%s\n%s", code, output, diagnostics)
		}
	}
}

// testcompactpasssummary retains warnings while making full comparisons opt-in
func TestCompactPassSummary(t *testing.T) {
	policy := testCheckPolicy(t, t.TempDir())
	report := testGateReport()
	report.ScoringVersion, report.Classification = scoringVersion, "excellent"
	report.Policy = evaluatePolicy(report, policy)
	report.Warnings = []string{"example analysis warning"}
	report.Files = []FileReport{{Path: "a.go", Score: 9, Findings: []Finding{
		{Rule: "undocumented-exported-symbols", Severity: "warning", Message: "one undocumented export"},
	}}}
	cfg := RunConfig{output: "compact", failUnder: -1, maxFindings: 10}
	var concise, verbose bytes.Buffer
	if err := emitConfiguredReport(&concise, cfg, report); err != nil {
		t.Fatal(err)
	}
	text := concise.String()
	for _, want := range []string{"Analysis: complete", "Project score: 9.0/10", "Model: " + scoringVersion,
		"Project files:", "Project policy: PASS", "Checks: 17/17 passed", "Warning: example analysis warning"} {
		if !strings.Contains(text, want) {
			t.Fatalf("passing summary missing %q:\n%s", want, text)
		}
	}
	for _, omitted := range []string{"Project dimensions:", "minimum_score:", "Findings:", "a.go"} {
		if strings.Contains(text, omitted) {
			t.Fatalf("routine passing output included %q:\n%s", omitted, text)
		}
	}
	cfg.verbose = true
	if err := emitConfiguredReport(&verbose, cfg, report); err != nil {
		t.Fatal(err)
	}
	if strings.Count(verbose.String(), "[PASS]") != 17 || !strings.Contains(verbose.String(), "Project dimensions:") ||
		!strings.Contains(verbose.String(), "Findings: 1 shown / 1 available") {
		t.Fatalf("verbose output lost full details:\n%s", verbose.String())
	}
	if concise.Len()*2 >= verbose.Len() {
		t.Fatalf("passing summary did not halve verbose output bytes: %d vs %d", concise.Len(), verbose.Len())
	}

	cfg.verbose = false
	for _, tc := range []struct {
		name   string
		modify func(*Report, *RunConfig)
		want   string
	}{
		{"unavailable", func(r *Report, _ *RunConfig) {
			r.Dimensions.Coupling = nil
			r.Policy = evaluatePolicy(*r, policy)
		}, "minimum_dimensions.coupling: N/A >= 9 [UNAVAILABLE]"},
		{"partial", func(r *Report, _ *RunConfig) {
			r.AnalysisStatus = "partial"
			r.Policy = evaluatePolicy(*r, policy)
		}, "Project policy: ERROR"},
		{"cli-failure", func(_ *Report, c *RunConfig) {
			c.failUnder = 10
		}, "CLI score gate: FAIL"},
		{"selected", func(r *Report, _ *RunConfig) {
			r.Selection = &ReportSelection{Kind: "file", Path: "a.go", CodeHealthScore: 9}
		}, "Selected file: a.go"},
		{"unchecked", func(r *Report, _ *RunConfig) {
			r.Policy = nil
		}, "Project policy: NOT CHECKED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, config := report, cfg
			tc.modify(&selected, &config)
			var output bytes.Buffer
			if err := emitConfiguredReport(&output, config, selected); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{tc.want, "Findings:", "Warning: example analysis warning"} {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("required context hidden: %q\n%s", want, output.String())
				}
			}
		})
	}
}

// testcompactverbosecli preserves exit codes and cached full-report contracts
func TestCompactVerboseCLI(t *testing.T) {
	root := testViewProject(t)
	writeFixture(t, root, projectConfigFileName, `{"version":1,"minimum_score":0,"health_thresholds":{"maximum_panics":1}}`)
	for _, verbose := range []bool{false, true} {
		args := []string{"--path", root, "--check", "--format", "compact"}
		if verbose {
			args = append(args, "--verbose", "--cache-debug")
		}
		code, output, diagnostics := runHealthCLI(t, args...)
		if code != 0 || !strings.Contains(output, "Checks: 2/2 passed") ||
			strings.Contains(output, "minimum_score:") != verbose || strings.Contains(output, "Findings:") != verbose {
			t.Fatalf("CLI verbosity changed policy or lost detail: %d\n%s\n%s", code, output, diagnostics)
		}
		if verbose && !strings.Contains(diagnostics, "HIT") {
			t.Fatalf("presentation-only flag invalidated the cache: %s", diagnostics)
		}
	}
	for _, format := range []string{"json", "text"} {
		args := []string{"--path", root, "--check", "--format", format}
		code, plain, diagnostics := runHealthCLI(t, args...)
		if code != 0 {
			t.Fatalf("%s failed: %s", format, diagnostics)
		}
		code, verbose, diagnostics := runHealthCLI(t, append(args, "--verbose")...)
		if code != 0 || plain != verbose {
			t.Fatalf("verbose changed %s contract: %d\n%s", format, code, diagnostics)
		}
	}
	writeFixture(t, root, projectConfigFileName, `{"version":1,"minimum_score":0,"health_thresholds":{"maximum_panics":0}}`)
	for _, args := range [][]string{nil, {"--verbose"}} {
		code, output, diagnostics := runHealthCLI(t, append([]string{"--path", root, "--check", "--format", "compact", "--max-findings", "0"}, args...)...)
		if code != 3 || !strings.Contains(output, "maximum_panics: 1 <= 0 [FAIL]") {
			t.Fatalf("verbosity hid policy failure: %d\n%s\n%s", code, output, diagnostics)
		}
	}
}

// testcompactfindingpriority ranks gate evidence before severity and low-scoring unrelated files
func TestCompactFindingPriority(t *testing.T) {
	report := Report{
		Policy: &PolicyResult{Status: "FAIL", Checks: []PolicyCheck{
			{Name: "minimum_dimensions.complexity", Status: "FAIL", Threshold: 9},
			{Name: "maximum_panics", Status: "FAIL", Threshold: 0},
		}},
		Files: []FileReport{
			{Path: "a.go", Score: 1, Findings: []Finding{
				{Rule: "unknown", Message: "unknown"},
				{Rule: "stale-recursion-directive", Severity: "info"},
				{Rule: "undocumented-exported-symbols", Severity: "warning"},
				{Rule: "panic-usage", Severity: "high"},
			}},
			{Path: "b.go", Score: 8, Findings: []Finding{{Rule: "high-complexity", Severity: "warning"}}},
			{Path: "c.go", Score: 9, Findings: []Finding{
				{Rule: "high-complexity", Severity: "high", Line: 12, Message: "b"},
				{Rule: "high-complexity", Severity: "high", Line: 12, Message: "a"},
				{Rule: "deep-nesting", Severity: "high", Line: 12},
				{Rule: "high-complexity", Severity: "high", Line: 5},
			}},
			{Path: "d.go", Score: 0, Dimensions: Dimensions{Complexity: ptr(1)}},
			{Path: "z.go", Score: 9, Findings: []Finding{{Rule: "high-complexity", Severity: "high", Line: 1}}},
		},
	}
	got := compactFindings(report)
	wantPaths := []string{"c.go", "c.go", "c.go", "c.go", "z.go", "b.go", "a.go", "a.go", "a.go", "a.go", "d.go"}
	for i, path := range wantPaths {
		if len(got) != len(wantPaths) || got[i].path != path {
			t.Fatalf("wrong priority at %d: %+v", i, got)
		}
	}
	if got[0].finding.Line != 5 || got[1].finding.Rule != "deep-nesting" ||
		got[2].finding.Message != "a" || got[3].finding.Message != "b" || !got[len(got)-1].synthetic {
		t.Fatalf("tie-breaking or measured-first ordering failed: %+v", got)
	}
	for i, j := 0, len(report.Files)-1; i < j; i, j = i+1, j-1 {
		report.Files[i], report.Files[j] = report.Files[j], report.Files[i]
	}
	for _, file := range report.Files {
		for i, j := 0, len(file.Findings)-1; i < j; i, j = i+1, j-1 {
			file.Findings[i], file.Findings[j] = file.Findings[j], file.Findings[i]
		}
	}
	if shuffled := compactFindings(report); !reflect.DeepEqual(got, shuffled) {
		t.Fatal("presentation order depends on input order")
	}
}

// testgatefindingfamilies checks relevance without preferring unrelated severe findings
func TestGateFindingFamilies(t *testing.T) {
	for _, tc := range []struct {
		rule string
		gate string
	}{
		{"high-complexity", "minimum_dimensions.complexity"},
		{"goto-usage", "minimum_dimensions.complexity"},
		{"undocumented-recursion", "minimum_dimensions.complexity"},
		{"deep-nesting", "minimum_dimensions.structure"},
		{"long-function", "minimum_dimensions.structure"},
		{"large-file", "minimum_score"},
		{"duplicate-blocks", "minimum_dimensions.duplication"},
		{"high-coupling", "minimum_dimensions.coupling"},
		{"import-cycle", "minimum_dimensions.cyclic_dependencies"},
		{"interface-compliance", "minimum_dimensions.interface_compliance"},
		{"duck-typing", "minimum_dimensions.duck_typing"},
		{"undocumented-exported-symbols", "minimum_documentation_percent"},
		{"ignored-return-value", "maximum_ignored_return_values"},
		{"panic-usage", "maximum_panics"},
		{"high-git-churn", "maximum_git_churn"},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			report := Report{
				Policy: &PolicyResult{Status: "FAIL", Checks: []PolicyCheck{{Name: tc.gate, Status: "FAIL"}}},
				Files: []FileReport{
					{Path: "a.go", Score: 1, Findings: []Finding{{Rule: "unrelated", Severity: "high"}}},
					{Path: "b.go", Score: 9, Findings: []Finding{{Rule: tc.rule, Severity: "warning"}}},
				},
			}
			if got := compactFindings(report); len(got) != 2 || got[0].path != "b.go" {
				t.Fatalf("relevant evidence lost priority: %+v", got)
			}
		})
	}
}

// testinvestigationfallback avoids redundant hints and selects the largest uncovered deficit
func TestInvestigationFallback(t *testing.T) {
	report := Report{
		Policy: &PolicyResult{Status: "FAIL", Checks: []PolicyCheck{
			{Name: "minimum_score", Status: "FAIL", Threshold: 9},
			{Name: "minimum_dimensions.complexity", Status: "FAIL", Threshold: 9},
			{Name: "minimum_dimensions.structure", Status: "FAIL", Threshold: 9},
		}},
		Files: []FileReport{
			{Path: "hint.go", Score: 8, Dimensions: Dimensions{Complexity: ptr(8), Structure: ptr(7)}},
			{Path: "measured.go", Score: 8, Dimensions: Dimensions{Complexity: ptr(8), Structure: ptr(7)}, Findings: []Finding{
				{Rule: "deep-nesting", Severity: "warning"},
			}},
			{Path: "overall.go", Score: 8},
			{Path: "healthy.go", Score: 10, Dimensions: Dimensions{Complexity: ptr(10), Structure: ptr(10)}},
		},
	}
	got := compactFindings(report)
	if len(got) != 3 || got[0].path != "measured.go" || got[0].synthetic ||
		got[1].path != "hint.go" || !strings.Contains(got[1].finding.Message, "structure 7.0") ||
		got[2].finding.Rule != "score-investigation" {
		t.Fatalf("fallback hints obscured or duplicated measured evidence: %+v", got)
	}
}

// reporterrorwriter injects output failures
type reportErrorWriter struct {
	err error
}

// write returns the configured output error
func (w reportErrorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

// testreportoutputerrors surfaces write failures and preserves closed-pipe handling
func TestReportOutputErrors(t *testing.T) {
	for _, format := range []string{"json", "compact", "text"} {
		cfg := RunConfig{output: format}
		if err := emitConfiguredReport(reportErrorWriter{err: errors.New("write failed")}, cfg, Report{}); err == nil {
			t.Fatalf("%s silently swallowed output failure", format)
		}
		if err := emitConfiguredReport(reportErrorWriter{err: io.ErrClosedPipe}, cfg, Report{}); err != nil {
			t.Fatalf("%s changed closed-pipe behavior: %v", format, err)
		}
	}
}

// testselectionboundaries rejects outside symlinks and supports the root package
func TestSelectionBoundaries(t *testing.T) {
	root := testViewProject(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := selectionPath(root, "outside"); err == nil {
		t.Fatal("outside symlink accepted")
	}
	writeFixture(t, root, "root.go", "package view\n")
	report, err := analyze(root, false)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := selectReport(report, "", ".")
	if err != nil || selected.Selection.Path != "." || len(selected.Files) != 1 {
		t.Fatalf("root package selection failed: %+v %v", selected.Selection, err)
	}
	unchanged, err := selectReport(report, "", "")
	if err != nil || !reflect.DeepEqual(unchanged, report) {
		t.Fatal("unscoped report changed")
	}
}
