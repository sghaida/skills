package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestParseGitChurnIgnoresInvalidEntries(t *testing.T) {
	churn := parseGitChurn("10\t2\tcmd/main.go\n-\t-\tbinary\nbad\t1\tignored.go\n")
	if churn["cmd/main.go"] != 12 {
		t.Fatalf("want parsed churn of 12, got %d", churn["cmd/main.go"])
	}
	if _, ok := churn["binary"]; ok {
		t.Fatal("binary entry should be ignored")
	}
}

func TestTopGitHotspotsSortsAndLimitsResults(t *testing.T) {
	files := make([]FileReport, 6)
	for i := range files {
		files[i] = FileReport{
			Path:   string(rune('a' + i)),
			Health: HealthMetrics{GitChurn: i},
		}
	}

	result := topGitHotspots(files)
	if len(result) != 5 {
		t.Fatalf("want five hotspots, got %d", len(result))
	}
	if result[0].Health.GitChurn != 5 || result[4].Health.GitChurn != 1 {
		t.Fatalf("unexpected hotspot ordering: %+v", result)
	}
}

func TestPrepareAnalysisRejectsMissingPath(t *testing.T) {
	scan, err := resolveScanContext("/path/that/does/not/exist", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareAnalysisContext(scan); err == nil {
		t.Fatal("expected missing path error")
	}
}

func TestFinalizeDuplicationSummarySkipsExternalEngine(t *testing.T) {
	summary := Summary{}
	file := &sourceFile{duplicateTokens: 10, totalFunctionTokens: 20}
	finalizeDuplicationSummary([]*sourceFile{file}, duplicationEngineJSCPDRS, &summary)
	if summary.DuplicateTokens != 0 || summary.FunctionTokens != 0 {
		t.Fatalf("external engine should not update internal totals: %+v", summary)
	}
}

// testnestedmodulechurngate retains scan-relative history and excludes sibling files
func TestNestedModuleChurnGate(t *testing.T) {
	project := newParityProject(t)
	project.write("nested/go.mod", "module example.com/nested\n")
	project.write("nested/main.go", "package nested\nfunc value() int { return 1 }\n")
	project.write("sibling.go", "package sibling\nfunc other() {}\n")
	project.write("nested/"+projectConfigFileName,
		`{"version":1,"minimum_score":0,"health_thresholds":{"maximum_git_churn":0}}`)
	project.git("init", "-q")
	project.git("add", ".")
	project.git("-c", "commit.gpgsign=false", "commit", "-q", "-m", "initial fixture",
		"-m", "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>")
	project.write("nested/main.go", "package nested\nfunc value() int { return 2 }\n")
	project.git("add", ".")
	project.git("-c", "commit.gpgsign=false", "commit", "-q", "-m", "change fixture",
		"-m", "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>")
	cache := filepath.Join(t.TempDir(), "cache.json")
	for _, flags := range [][]string{{"--no-cache"}, nil} {
		args := append([]string{"--path", filepath.Join(project.root, "nested"), "--check", "--format", "json", "--cache-file", cache}, flags...)
		code, output, diagnostics := runHealthCLI(t, args...)
		if code != 3 {
			t.Fatalf("nested churn gate falsely passed: %d %s", code, diagnostics)
		}
		var report Report
		if err := json.Unmarshal([]byte(output), &report); err != nil {
			t.Fatal(err)
		}
		if !report.Configuration.IncludeGitChurn || report.Summary.Health.GitChurn != 4 ||
			len(report.Files) != 1 || report.Files[0].Health.GitChurn != 4 {
			t.Fatalf("expected 4 changed lines from nested history only: %+v", report.Summary.Health)
		}
		if report.Policy == nil || report.Policy.Status != "FAIL" ||
			report.Policy.Checks[1].Name != "maximum_git_churn" ||
			report.Policy.Checks[1].Measured == nil || *report.Policy.Checks[1].Measured != 4 {
			t.Fatalf("incorrect native churn comparison: %+v", report.Policy)
		}
	}
}
