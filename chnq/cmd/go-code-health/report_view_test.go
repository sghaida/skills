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
		"maximum_panics: 1 <= 0 [FAIL]", "Findings: 1 shown / 4 available", "- a.go:12 [long-function]"} {
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
