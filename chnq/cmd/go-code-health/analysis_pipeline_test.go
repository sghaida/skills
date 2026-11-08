package main

import (
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
