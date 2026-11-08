package main

import (
	"strings"
	"testing"
)

func TestIdentifyChangedFiles(t *testing.T) {
	oldHashes := map[string]string{
		"file1.go": "hash1",
		"file2.go": "hash2",
		"file3.go": "hash3",
	}

	newHashes := map[string]string{
		"file1.go": "hash1",     // unchanged
		"file2.go": "hash2_new", // changed
		"file4.go": "hash4",     // new
		// file3 is deleted
	}

	changed := IdentifyChangedFiles(oldHashes, newHashes)

	if !changed["file2.go"] {
		t.Error("expected file2.go to be marked as changed")
	}
	if !changed["file3.go"] {
		t.Error("expected file3.go to be marked as changed (deleted)")
	}
	if !changed["file4.go"] {
		t.Error("expected file4.go to be marked as changed (new)")
	}
	if changed["file1.go"] {
		t.Error("expected file1.go to NOT be marked as changed")
	}
}

func TestMergeReports_NoOldCache(t *testing.T) {
	// Test merging when there's no old cache
	newReport := &Report{
		SchemaVersion:  "1.0",
		ScoringVersion: "v1",
		Language:       "go",
		MaxScore:       10,
		Files: []FileReport{
			{
				Path:    "file1.go",
				Package: "pkg1",
				Score:   8.5,
				Lines:   100,
			},
		},
		Hotspots: []FileReport{},
	}

	update := NewIncrementalUpdate(nil, newReport, map[string]bool{})
	merged := update.MergeReports()

	if len(merged.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(merged.Files))
	}
	if merged.Files[0].Path != "file1.go" {
		t.Errorf("expected file1.go, got %s", merged.Files[0].Path)
	}
}

func TestMergeReports_WithOldCache(t *testing.T) {
	// Old report has 2 files
	oldReport := &Report{
		Files: []FileReport{
			{
				Path:    "file1.go",
				Package: "pkg1",
				Score:   7.0,
				Lines:   100,
			},
			{
				Path:    "file2.go",
				Package: "pkg1",
				Score:   6.0,
				Lines:   150,
			},
		},
	}

	oldCache := &CacheFile{
		Report: oldReport,
	}

	// New report has 2 files (one changed)
	newReport := &Report{
		SchemaVersion:  "1.0",
		ScoringVersion: "v1",
		Language:       "go",
		MaxScore:       10,
		Files: []FileReport{
			{
				Path:    "file2.go",
				Package: "pkg1",
				Score:   7.5, // improved
				Lines:   150,
			},
		},
		Hotspots: []FileReport{},
	}

	// file1.go unchanged, file2.go changed
	changedFiles := map[string]bool{
		"file2.go": true,
	}

	update := NewIncrementalUpdate(oldCache, newReport, changedFiles)
	merged := update.MergeReports()

	if len(merged.Files) != 2 {
		t.Errorf("expected 2 files in merged report, got %d", len(merged.Files))
	}

	// Check that old file1 is preserved
	found := false
	for _, f := range merged.Files {
		if f.Path == "file1.go" && f.Score == 7.0 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected file1.go with score 7.0 to be preserved from old cache")
	}

	// Check that new file2 is used
	found = false
	for _, f := range merged.Files {
		if f.Path == "file2.go" && f.Score == 7.5 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected file2.go with new score 7.5")
	}
}

func TestMergeReports_HotspotIdentification(t *testing.T) {
	// New report with files that should be identified as hotspots (score < 8)
	newReport := &Report{
		SchemaVersion:  "1.0",
		ScoringVersion: "v1",
		Language:       "go",
		MaxScore:       10,
		Files: []FileReport{
			{
				Path:    "good.go",
				Package: "pkg1",
				Score:   9.0,
			},
			{
				Path:    "hotspot1.go",
				Package: "pkg1",
				Score:   7.5,
			},
			{
				Path:    "hotspot2.go",
				Package: "pkg1",
				Score:   4.0,
			},
		},
		Hotspots: []FileReport{},
	}

	update := NewIncrementalUpdate(nil, newReport, map[string]bool{})
	merged := update.MergeReports()

	if len(merged.Hotspots) != 2 {
		t.Errorf("expected 2 hotspots, got %d", len(merged.Hotspots))
	}

	// Verify hotspots are sorted by score (lowest first)
	if merged.Hotspots[0].Score > merged.Hotspots[1].Score {
		t.Error("hotspots should be sorted by score (ascending)")
	}
}

func TestHotspotsExplainer_GenerateExplanations(t *testing.T) {
	// Create a simple report with hotspots
	complexity := 3.5
	structure := 4.0
	report := &Report{
		ScoringVersion: "v1",
		Hotspots: []FileReport{
			{
				Path:  "hotspot.go",
				Score: 5.0,
				Lines: 250,
				Functions: []FunctionReport{
					{
						Name:                "processData",
						Lines:               120,
						BranchingComplexity: 8,
						MaxNesting:          5,
					},
				},
				DuplicationPercent:  15.0,
				DuplicateTokens:     300,
				TotalFunctionTokens: 2000,
				InternalImports:     5,
				Dimensions: Dimensions{
					Complexity: &complexity,
					Structure:  &structure,
				},
			},
		},
	}

	explainer := NewHotspotsExplainer(report, "file")
	explanation, err := explainer.GenerateExplanations()

	if err != nil {
		t.Errorf("GenerateExplanations failed: %v", err)
	}

	if explanation == "" {
		t.Error("expected non-empty explanation")
	}

	// Verify key content is present
	if !contains(explanation, "Hotspot Summary") {
		t.Error("expected 'Hotspot Summary' in explanation")
	}
	if !contains(explanation, "hotspot.go") {
		t.Error("expected hotspot file path in explanation")
	}
	if !contains(explanation, "250-line file") {
		t.Error("expected file size in explanation")
	}
	if !contains(explanation, "processData") {
		t.Error("expected function name in explanation")
	}
}

func TestHotspotsExplainer_SummaryTable(t *testing.T) {
	complexity := 5.0
	structure := 6.0
	duplication := 7.0
	coupling := 8.0

	report := &Report{
		ScoringVersion: "v1",
		Hotspots: []FileReport{
			{
				Path:  "file1.go",
				Score: 6.5,
				Dimensions: Dimensions{
					Complexity:  &complexity,
					Structure:   &structure,
					Duplication: &duplication,
					Coupling:    &coupling,
				},
			},
		},
	}

	explainer := NewHotspotsExplainer(report, "file")
	explanation, err := explainer.GenerateExplanations()

	if err != nil {
		t.Errorf("GenerateExplanations failed: %v", err)
	}

	// Verify table is present
	if !contains(explanation, "| Hotspot | Overall | Complexity | Structure | Duplication | Coupling |") {
		t.Error("expected table header in explanation")
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestIncrementalAggregateMetrics(t *testing.T) {
	complexity := 5.0
	structure := 6.0
	report := &Report{
		Summary: Summary{ExcludedTestFiles: 2},
		Files: []FileReport{
			{Path: "a.go", Lines: 10, Functions: []FunctionReport{{Name: "A"}}, DuplicateTokens: 2, TotalFunctionTokens: 20,
				Dimensions: Dimensions{Complexity: &complexity, Structure: &structure},
				Health:     HealthMetrics{ExportedSymbols: 2, DocumentedExportedSymbols: 1}},
			{Path: "b.go", Lines: 20, Functions: []FunctionReport{{Name: "B"}}, DuplicateTokens: 1, TotalFunctionTokens: 10,
				Dimensions: Dimensions{Complexity: &complexity, Structure: &structure}},
		},
	}
	rebuildAggregateMetrics(report)
	if report.Summary.GoFiles != 2 || report.Summary.SourceLines != 30 || report.Summary.ExcludedTestFiles != 2 {
		t.Fatalf("unexpected summary: %+v", report.Summary)
	}
	if report.CodeHealthScore == nil || report.Summary.Health.DocumentationPercent != 50 {
		t.Fatalf("aggregate metrics not rebuilt: %+v", report)
	}
}
