package main

import (
	"strings"
	"testing"
)

func TestPackageHotspotExplanation(t *testing.T) {
	complexity := 4.0
	report := &Report{
		ScoringVersion: scoringVersion,
		Files: []FileReport{
			{Path: "pkg/a.go", Package: "pkg", Score: 4, Lines: 20, Dimensions: Dimensions{Complexity: &complexity}},
			{Path: "pkg/b.go", Package: "pkg", Score: 6, Lines: 30, Dimensions: Dimensions{Complexity: &complexity}},
			{Path: "other.go", Package: ".", Score: 9, Lines: 10, Dimensions: Dimensions{Complexity: &complexity}},
		},
		Hotspots: []FileReport{{Path: "pkg/a.go", Package: "pkg", Score: 4, Dimensions: Dimensions{Complexity: &complexity}}},
	}
	explanation, err := NewHotspotsExplainer(report, "package").GenerateExplanations()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(explanation, "| pkg |") || strings.Contains(explanation, "| other.go |") {
		t.Fatalf("unexpected package explanation:\n%s", explanation)
	}
}
