package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmitReportAndCacheHashMaps(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "a.go", "package demo\nfunc A() {}\n")
	writeFixture(t, root, "b.go", "package demo\nfunc B() {}\n")
	manager := NewCacheManager(filepath.Join(root, "cache.json"), scoringVersion)
	hashes, err := manager.ComputeRepositoryFileHashes(root, false)
	if err != nil || len(hashes) != 2 {
		t.Fatalf("repository hashes: %v, %d", err, len(hashes))
	}
	direct := manager.ComputeFilesHashMap(root, []string{filepath.Join(root, "a.go")})
	if len(direct) != 1 {
		t.Fatalf("expected direct hash map entry, got %d", len(direct))
	}
	score := 9.0
	report := Report{CodeHealthScore: &score, AnalysisStatus: "complete"}
	var jsonOutput bytes.Buffer
	if err := emitReport("json", report); err != nil {
		t.Fatal(err)
	}
	_ = jsonOutput
	if err := emitReport("text", report); err != nil {
		t.Fatal(err)
	}
	malformed := filepath.Join(root, "malformed.json")
	writeFixture(t, root, "malformed.json", "{")
	if _, err := NewCacheManager(malformed, scoringVersion).ReadCache(); err == nil {
		t.Fatal("expected malformed cache error")
	}
	if _, err := manager.ComputeFileHash(filepath.Join(root, "missing.go")); err == nil {
		t.Fatal("expected missing file hash error")
	}
	if _, err := manager.ComputeRepositoryFileHashes(filepath.Join(root, "missing"), false); err == nil {
		t.Fatal("expected missing repository error")
	}
	writeFixture(t, root, "cache-target", "not a directory")
	failing := NewCacheManager(filepath.Join(root, "cache-target", "cache.json"), scoringVersion)
	if err := failing.WriteCache(&report, "hash", 1, root); err == nil {
		t.Fatal("expected cache write error")
	}
	cacheDir := filepath.Join(root, "cache-dir")
	if err := os.Mkdir(cacheDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := NewCacheManager(cacheDir, scoringVersion).WriteCache(&report, "hash", 1); err == nil {
		t.Fatal("expected cache rename error")
	}
}
func TestComputeRepositoryHashIgnoresBuildArtifacts(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "main.go", "package main\n")
	writeFixture(t, root, ".build/ignored.go", "package ignored\n")
	manager := NewCacheManager("", scoringVersion)
	first, err := manager.ComputeRepositoryHash(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, ".build/ignored.go", "package changed\n")
	second, err := manager.ComputeRepositoryHash(root)
	if err != nil || first != second {
		t.Fatalf("build artifact changed repository hash: %q %q %v", first, second, err)
	}
}
func TestHotspotBehaviorPatternsAndCacheErrors(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "handler.go")
	writeFixture(t, root, "handler.go", `package handler
import ("context"; "errors")
func Handle(ctx context.Context, err error) int {
	statusCode := 408
	if ctx.Err() != nil { return 408 }
	if errors.Is(err, context.Canceled) { return 499 }
	return statusCode
}
`)
	value := 4.0
	report := &Report{ScoringVersion: scoringVersion, Hotspots: []FileReport{
		{Path: path, Score: value, Lines: 8, Dimensions: Dimensions{Complexity: &value}},
	}}
	explanation, err := NewHotspotsExplainer(report, "file").GenerateExplanations()
	if err != nil || !strings.Contains(explanation, "Context handling") || !strings.Contains(explanation, "HTTP status codes") {
		t.Fatalf("behavior extraction failed: %v\n%s", err, explanation)
	}
	manager := NewCacheManager(filepath.Join(root, "cache.json"), scoringVersion)
	if _, err := manager.ReadCache(); err == nil {
		t.Fatal("expected missing cache error")
	}
	if _, err := manager.ValidateCache("hash"); err == nil {
		t.Fatal("expected validation error for missing cache")
	}
	if _, err := manager.ComputeRepositoryFileHashes(filepath.Join(root, "missing"), false); err == nil {
		t.Fatal("expected repository hash error")
	}
}
