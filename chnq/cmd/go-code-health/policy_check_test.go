package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testcheckpolicy builds a gate with every supported threshold
func testCheckPolicy(t *testing.T, root string) *checkPolicy {
	t.Helper()
	writeFixture(t, root, projectConfigFileName, `{
		"version":1,"duplication_engine":"internal","minimum_score":9,
		"minimum_dimensions":{"complexity":9,"structure":9,"duplication":9,"coupling":9,
			"cyclic_dependencies":9,"interface_compliance":9,"duck_typing":9},
		"health_thresholds":{"minimum_documentation_percent":80,"maximum_ignored_return_values":0,
			"maximum_panics":0,"maximum_todos":0,"maximum_fixmes":0,"maximum_import_cycles":0,
			"maximum_dependency_depth":6,"maximum_incoming_dependencies":2,"maximum_git_churn":100},
		"include_tests":true,"goto_check":true,"max_function_lines":60,"recursion_check":true
	}`)
	policy, err := loadCheckPolicy(root)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

// testgatereport supplies complete measurements on the inclusive boundaries
func testGateReport() Report {
	return Report{
		AnalysisStatus: "complete", CodeHealthScore: ptr(9),
		Dimensions: Dimensions{
			Complexity: ptr(9), Structure: ptr(9), Duplication: ptr(9), Coupling: ptr(9),
			CyclicDependencies: ptr(9), InterfaceCompliance: ptr(9), DuckTyping: ptr(9),
		},
		Configuration: Configuration{IncludeGitChurn: true},
		Summary:       Summary{Health: HealthMetrics{DocumentationPercent: 80, GitChurn: 100}},
		Packages: []PackageReport{
			{Path: ".", DependencyDepth: 6, IncomingDependencies: 1},
			{Path: "lib", DependencyDepth: 1, IncomingDependencies: 2},
		},
	}
}

// testnativepolicychecks covers inclusive gates, unavailable data, and graph maxima
func TestNativePolicyChecks(t *testing.T) {
	policy := testCheckPolicy(t, t.TempDir())
	report := testGateReport()
	result := evaluatePolicy(report, policy)
	if result.Status != "PASS" || len(result.Checks) != 17 {
		t.Fatalf("inclusive checks failed: %+v", result)
	}
	report.Dimensions.Complexity = ptr(8.9)
	report.Summary.Health.Panics = 1
	report.Packages[1].IncomingDependencies = 3
	result = evaluatePolicy(report, policy)
	failures := 0
	for _, check := range result.Checks {
		if check.Status == "FAIL" {
			failures++
		}
	}
	if result.Status != "FAIL" || failures != 3 {
		t.Fatalf("expected dimension, panic, and package maximum failures: %+v", result)
	}
	report = testGateReport()
	report.Configuration.IncludeGitChurn = false
	report.Dimensions.Coupling = nil
	result = evaluatePolicy(report, policy)
	unavailable := 0
	for _, check := range result.Checks {
		if check.Status == "UNAVAILABLE" {
			unavailable++
		}
	}
	if result.Status != "FAIL" || unavailable != 4 {
		t.Fatalf("missing metrics passed or became zero: %+v", result)
	}
	report = testGateReport()
	report.AnalysisStatus = "partial"
	if got := evaluatePolicy(report, policy); got.Status != "ERROR" {
		t.Fatalf("partial analysis passed: %+v", got)
	}
	report.AnalysisStatus = "complete"
	report.CodeHealthScore = nil
	if got := evaluatePolicy(report, policy); got.Status != "ERROR" {
		t.Fatalf("missing score passed: %+v", got)
	}
	for _, value := range []*float64{nil, ptr(math.NaN()), ptr(math.Inf(1))} {
		if check := compareThreshold("minimum_score", value, 0); check.Status != "UNAVAILABLE" || check.Measured != nil {
			t.Fatalf("invalid measurement passed: %+v", check)
		}
	}
}

// testcheckpolicyvalidation rejects ambiguous or misspelled enforcement settings
func TestCheckPolicyValidation(t *testing.T) {
	root := t.TempDir()
	if _, err := loadCheckPolicy(root); err == nil {
		t.Fatal("missing policy passed")
	}
	for _, raw := range []string{
		`{`, `null`, `[]`, `{"version":2,"minimum_score":0}`,
		`{"version":1}`, `{"version":1,"minimum_score":null}`,
		`{"version":1,"minimum_score":11}`, `{"version":1,"minimum_score":-1}`,
		`{"version":1,"minimum_score":0,"include_tests":null}`,
		`{"version":1,"minimum_score":0,"minimum_dimensions":null}`,
		`{"version":1,"minimum_score":0,"minimum_dimensions":{"coupling":null}}`,
		`{"version":1,"minimum_score":0,"minimum_dimensions":{"typo":1}}`,
		`{"version":1,"minimum_score":0,"minimum_dimensions":{"complexity":-1}}`,
		`{"version":1,"minimum_score":0,"health_thresholds":{"maximum_panics":-1}}`,
		`{"version":1,"minimum_score":0,"health_thresholds":{"maximum_panics":1.5}}`,
		`{"version":1,"minimum_score":0,"health_thresholds":{"minimum_documentation_percent":101}}`,
		`{"version":1,"minimum_score":0,"health_thresholds":{"maximum_typo":0}}`,
		`{"version":1,"minimum_score":0,"typo":1}`,
		`{"version":1,"minimum_score":0} {}`,
		`{"version":1,"minimum_score":0,"max_function_lines":0}`,
	} {
		writeFixture(t, root, projectConfigFileName, raw)
		if _, err := loadCheckPolicy(root); err == nil {
			t.Fatalf("invalid policy accepted: %s", raw)
		}
	}
	writeFixture(t, root, projectConfigFileName, `{"version":1,"minimum_score":0,"health_thresholds":{"minimum_documentation_percent":80.5}}`)
	if _, err := loadCheckPolicy(root); err != nil {
		t.Fatalf("fractional percentage rejected: %v", err)
	}
	if err := os.Remove(filepath.Join(root, projectConfigFileName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, projectConfigFileName), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCheckPolicy(root); err == nil {
		t.Fatal("unreadable policy passed")
	}
}

// testpreparepresentation preserves project scope and applies policy scan options
func TestPreparePresentation(t *testing.T) {
	root, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "go.mod", "module example.com/gate\n")
	writeFixture(t, root, "lib/a.go", "package lib\n")
	testCheckPolicy(t, root)
	cfg, err := preparePresentation(RunConfig{path: root, check: true, maxFindings: 5})
	if err != nil || !cfg.includeTests || !cfg.includeGitChurn || cfg.policy == nil {
		t.Fatalf("policy options not resolved before scanning: %+v %v", cfg, err)
	}
	for _, tc := range []struct {
		cfg  RunConfig
		want string
	}{
		{RunConfig{maxFindings: -1}, "nonnegative"},
		{RunConfig{file: "a.go", pkg: "."}, "mutually exclusive"},
		{RunConfig{check: true, cacheInfo: true}, "cannot be combined"},
		{RunConfig{path: filepath.Join(root, "missing"), check: true}, "project root"},
		{RunConfig{path: filepath.Join(root, "lib/a.go"), check: true}, "project directory"},
		{RunConfig{path: filepath.Join(root, "lib"), pkg: "."}, "module root"},
		{RunConfig{path: root, check: true, includeTestsSet: true}, "conflicts"},
		{RunConfig{path: t.TempDir(), check: true}, "requires .go-code-health.json"},
	} {
		if _, err := preparePresentation(tc.cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("expected %q, got %v", tc.want, err)
		}
	}
	if _, err := preparePresentation(RunConfig{}); err != nil {
		t.Fatalf("legacy options rejected: %v", err)
	}
	if _, err := preparePresentation(RunConfig{path: root, check: true, includeTestsSet: true, includeTests: true}); err != nil {
		t.Fatalf("matching explicit option rejected: %v", err)
	}
	if _, err := preparePresentation(RunConfig{path: root, file: "lib/a.go"}); err != nil {
		t.Fatalf("file selection rejected: %v", err)
	}
	for _, floor := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if validateFailUnder(floor) == nil {
			t.Fatal("nonfinite score threshold accepted")
		}
	}
}
