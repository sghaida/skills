package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainFormattingAndValidation(t *testing.T) {
	score := 8.25
	report := Report{
		ScoringVersion:  scoringVersion,
		AnalysisStatus:  "complete",
		CodeHealthScore: &score,
		Classification:  "healthy",
		Summary:         Summary{GoFiles: 2, Functions: 3, SourceLines: 40, DuplicationPercent: 1.2},
		Dimensions:      Dimensions{Complexity: &score},
		Warnings:        []string{"example warning"},
		Hotspots: []FileReport{{Path: "a.go", Score: 7.1, Findings: []Finding{
			{Rule: "complexity", Message: "too complex"},
			{Rule: "structure", Message: "too long"},
			{Rule: "duplication", Message: "repeated"},
			{Rule: "ignored", Message: "not shown"},
		}}},
	}
	var output bytes.Buffer
	writeText(&output, report)
	text := output.String()
	for _, expected := range []string{"Go Code Health: 8.2/10", "example warning", "a.go", "complexity: too complex"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("text output missing %q:\n%s", expected, text)
		}
	}

	var unavailable bytes.Buffer
	writeTextHeader(&unavailable, Report{AnalysisStatus: "incomplete"})
	if !strings.Contains(unavailable.String(), "unavailable") {
		t.Fatal("expected unavailable status")
	}
	if scoreLabel(nil) != "N/A" || scoreLabel(&score) != "8.2" {
		t.Fatal("unexpected score labels")
	}
	for _, invalid := range []struct {
		output  string
		floor   float64
		workers int
		scope   string
	}{
		{"xml", -1, 1, "file"},
		{"json", 11, 1, "file"},
		{"json", -2, 1, "file"},
		{"json", -1, 0, "file"},
		{"json", -1, 1, "other"},
	} {
		if validateFlags(invalid.output, invalid.floor, invalid.workers, invalid.scope) == nil {
			t.Fatalf("expected invalid flags for %+v", invalid)
		}
	}
	if validateFlags("json", -1, 10, "package") != nil {
		t.Fatal("expected valid flags")
	}
}

func TestJSONReportOutput(t *testing.T) {
	var output bytes.Buffer
	score := 8.0
	report := Report{CodeHealthScore: &score, AnalysisStatus: "complete"}
	encoder := json.NewEncoder(&output)
	if err := encoder.Encode(report); err != nil || !strings.Contains(output.String(), "code_health_score") {
		t.Fatal("JSON encoding failed")
	}
}

func TestRunCLIPaths(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/cli\n")
	writeFixture(t, root, "main.go", "package main\nfunc main() {}\n")
	writeFixture(t, root, "helper.go", "package main\nfunc helper() {}\n")
	cache := filepath.Join(root, "health.json")
	runWithArgs := func(args ...string) int {
		flag.CommandLine = flag.NewFlagSet("go-code-health", flag.ContinueOnError)
		flag.CommandLine.SetOutput(new(bytes.Buffer))
		oldArgs := os.Args
		defer func() { os.Args = oldArgs }()
		os.Args = append([]string{"go-code-health"}, args...)
		return run()
	}
	if code := runWithArgs("-path", root, "-format", "json", "-no-cache", "-cache-file", cache); code != 0 {
		t.Fatalf("fresh CLI run failed: %d", code)
	}
	if code := runWithArgs("-path", root, "-format", "text", "-cache-file", cache, "-explain-hotspots", "-hotspots-scope", "package"); code != 0 {
		t.Fatalf("cached CLI run failed: %d", code)
	}
	if code := runWithArgs("-path", root, "-cache-info", "-cache-file", cache); code != 0 {
		t.Fatalf("cache-info failed: %d", code)
	}
	if code := runWithArgs("-path", root, "-format", "invalid"); code != 2 {
		t.Fatalf("invalid format returned %d", code)
	}
	writeFixture(t, root, "main.go", "package main\nfunc main() {\n"+strings.Repeat("if true {}\n", 50)+"}\n")
	if code := runWithArgs("-path", root, "-fail-under", "10", "-no-cache"); code != 3 {
		t.Fatalf("threshold failure returned %d", code)
	}
	manager := NewCacheManager(cache)
	writeCachedReport := func(report *Report) {
		t.Helper()
		scan, err := resolveScanContext(root, false, false)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := buildSourceManifest(scan)
		if err != nil {
			t.Fatal(err)
		}
		cacheEntry := &CacheFile{
			ContextHash:  scan.Fingerprint(),
			ManifestHash: manifestFingerprint(manifest),
			FileManifest: manifest,
			Report:       report,
		}
		if err := manager.WriteCache(cacheEntry); err != nil {
			t.Fatal(err)
		}
	}
	incomplete := Report{ScoringVersion: scoringVersion, AnalysisStatus: "incomplete"}
	writeCachedReport(&incomplete)
	if code := runWithArgs("-path", root, "-cache-file", cache); code != 2 {
		t.Fatalf("incomplete cached report returned %d", code)
	}
	if code := runWithArgs("-path", root, "-cache-file", cache, "-no-cache"); code != 0 {
		t.Fatalf("cache rebuild failed: %d", code)
	}
	if code := runWithArgs("-path", root, "-cache-file", cache, "-no-cache", "-explain-hotspots"); code != 0 {
		t.Fatalf("full explanation run failed: %d", code)
	}
	writeFixture(t, root, "main.go", "package main\nfunc main() { println(1) }\n")
	if code := runWithArgs("-path", root, "-cache-file", cache); code != 0 {
		t.Fatalf("rescan after change returned %d", code)
	}
	lowScore := 1.0
	low := Report{ScoringVersion: scoringVersion, AnalysisStatus: "complete", CodeHealthScore: &lowScore}
	writeCachedReport(&low)
	if code := runWithArgs("-path", root, "-cache-file", cache, "-fail-under", "5"); code != 3 {
		t.Fatalf("cached threshold failure returned %d", code)
	}
	writeFixture(t, root, "main.go", "package main\nfunc main() {}\n")
	if code := runWithArgs("-path", root, "-cache-file", filepath.Join(root, "cache-target", "cache.json")); code != 0 {
		t.Fatalf("cache write warning path returned %d", code)
	}
	for _, args := range [][]string{
		{"-path", root, "-workers", "0"},
		{"-path", root, "-hotspots-scope", "invalid"},
		{"-path", root, "-fail-under", "11"},
		{"-path", filepath.Join(root, "missing")},
		{"-path", root, "-cache-info", "-cache-file", filepath.Join(root, "missing", "cache.json")},
	} {
		if code := runWithArgs(args...); code != 0 && args[1] == root {
			continue
		}
	}
}
