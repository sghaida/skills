package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestWorkerPoolCreation tests that a worker pool can be created with various worker counts
func TestWorkerPoolCreation(t *testing.T) {
	tests := []struct {
		name            string
		workers         int
		expectedWorkers int
	}{
		{"zero workers becomes 1", 0, 1},
		{"negative workers becomes 1", -5, 1},
		{"normal worker count", 10, 10},
		{"max worker count", 256, 256},
		{"over max capped at 256", 500, 256},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := NewWorkerPool(tt.workers)
			if pool.workers != tt.expectedWorkers {
				t.Errorf("expected %d workers, got %d", tt.expectedWorkers, pool.workers)
			}
			if pool.workChan == nil {
				t.Error("workChan should not be nil")
			}
			if pool.resultChan == nil {
				t.Error("resultChan should not be nil")
			}
			if pool.ctx == nil {
				t.Error("ctx should not be nil")
			}
			if pool.cancel == nil {
				t.Error("cancel should not be nil")
			}
		})
	}
}

// TestWorkerPoolProcessing tests that worker pool can process tasks
func TestWorkerPoolProcessing(t *testing.T) {
	pool := NewWorkerPool(2)

	// Mock analyze function
	analyzeFunc := func(_ context.Context, filePath string) (*FileReport, error) {
		return &FileReport{
			Path:  filePath,
			Score: 8.5,
		}, nil
	}

	pool.Start(analyzeFunc)

	// Submit tasks
	for i := 0; i < 5; i++ {
		pool.Submit(&AnalysisTask{
			FilePath:  "file" + string(rune(i)),
			FileIndex: i,
		})
	}

	// Collect results in a goroutine to avoid blocking
	results := make([]*AnalysisResult, 0, 5)
	resultsMu := sync.Mutex{}
	go func() {
		for result := range pool.Results() {
			resultsMu.Lock()
			results = append(results, result)
			resultsMu.Unlock()
		}
	}()

	pool.Wait()

	// Give results goroutine time to finish
	time.Sleep(10 * time.Millisecond)

	resultsMu.Lock()
	defer resultsMu.Unlock()

	if len(results) != 5 {
		t.Errorf("expected 5 results, got %d", len(results))
	}

	// Verify all results were processed
	for _, result := range results {
		if result.FileReport == nil {
			t.Error("FileReport should not be nil")
		}
		if result.Error != nil {
			t.Errorf("unexpected error: %v", result.Error)
		}
	}
}

// TestWorkerPoolError tests that errors are propagated
func TestWorkerPoolError(t *testing.T) {
	pool := NewWorkerPool(1)

	analyzeFunc := func(_ context.Context, filePath string) (*FileReport, error) {
		if strings.Contains(filePath, "error") {
			return nil, errors.New("test error")
		}
		return &FileReport{Path: filePath}, nil
	}

	pool.Start(analyzeFunc)

	pool.Submit(&AnalysisTask{FilePath: "errorfile", FileIndex: 0})
	pool.Submit(&AnalysisTask{FilePath: "goodfile", FileIndex: 1})

	// Collect results in a goroutine
	results := make([]*AnalysisResult, 0, 2)
	resultsMu := sync.Mutex{}
	go func() {
		for result := range pool.Results() {
			resultsMu.Lock()
			results = append(results, result)
			resultsMu.Unlock()
		}
	}()

	pool.Wait()

	// Give results goroutine time to finish
	time.Sleep(10 * time.Millisecond)

	resultsMu.Lock()
	defer resultsMu.Unlock()

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	// Check for error result
	errorFound := false
	for _, result := range results {
		if result.Error != nil {
			errorFound = true
			if !strings.Contains(result.Error.Error(), "test error") {
				t.Errorf("expected 'test error', got %v", result.Error)
			}
		}
	}

	if !errorFound {
		t.Error("expected at least one error result")
	}
}

// TestWorkerPoolContextCancellation tests that pool respects context cancellation
func TestWorkerPoolContextCancellation(_ *testing.T) {
	pool := NewWorkerPool(1)

	analyzeFunc := func(ctx context.Context, _ string) (*FileReport, error) {
		// Simulate work that checks context
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		return &FileReport{}, nil
	}

	pool.Start(analyzeFunc)
	pool.Submit(&AnalysisTask{FilePath: "test", FileIndex: 0})

	// Give time for task to start
	time.Sleep(10 * time.Millisecond)

	// Cancel the pool
	pool.Stop()
}

// TestCacheManagerCreation tests cache manager creation
func TestCacheManagerCreation(t *testing.T) {
	tests := []struct {
		name         string
		cacheFile    string
		expectedFile string
	}{
		{"default cache file", "", ".chnq-health-status"},
		{"custom cache file", "custom-cache.json", "custom-cache.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := NewCacheManager(tt.cacheFile, "v1.0.0")
			if cm.cacheFile != tt.expectedFile {
				t.Errorf("expected cache file %s, got %s", tt.expectedFile, cm.cacheFile)
			}
			if cm.version != "v1.0.0" {
				t.Errorf("expected version v1.0.0, got %s", cm.version)
			}
		})
	}
}

// TestComputeFileHash tests that file hashing works correctly
func TestComputeFileHash(t *testing.T) {
	cm := NewCacheManager("", "v1.0.0")

	// Create a temporary test file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("test content for hashing")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	hash1, err := cm.ComputeFileHash(testFile)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if hash1 == "" {
		t.Error("hash should not be empty")
	}

	// Verify hash is consistent
	hash2, err := cm.ComputeFileHash(testFile)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("hashes should be consistent: %s != %s", hash1, hash2)
	}

	// Test nonexistent file
	_, err = cm.ComputeFileHash(filepath.Join(tmpDir, "nonexistent"))
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

// TestComputeRepositoryHash tests repository hashing
func TestComputeRepositoryHash(t *testing.T) {
	cm := NewCacheManager("", "v1.0.0")

	tmpDir := t.TempDir()

	// Create test Go files
	file1 := filepath.Join(tmpDir, "file1.go")
	file2 := filepath.Join(tmpDir, "file2.go")
	ignored := filepath.Join(tmpDir, ".hidden.go")

	if err := os.WriteFile(file1, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.WriteFile(file2, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.WriteFile(ignored, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	hash, err := cm.ComputeRepositoryHash(tmpDir)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if hash == "" {
		t.Error("hash should not be empty")
	}

	// Verify hash is consistent
	hash2, err := cm.ComputeRepositoryHash(tmpDir)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if hash != hash2 {
		t.Errorf("hashes should be consistent: %s != %s", hash, hash2)
	}
}

// TestCacheWriteAndRead tests cache write and read operations
func TestCacheWriteAndRead(t *testing.T) {
	tmpDir := t.TempDir()
	cacheFile := filepath.Join(tmpDir, "cache.json")
	cm := NewCacheManager(cacheFile, "v1.0.0")

	// Create a test report
	score := 8.5
	report := &Report{
		SchemaVersion:   "v1",
		ScoringVersion:  "v1.0.0",
		CodeHealthScore: &score,
		Files: []FileReport{
			{
				Path:  "test.go",
				Score: 8.5,
			},
		},
	}

	// Write cache
	err := cm.WriteCache(report, "testhash123", 1000)
	if err != nil {
		t.Errorf("unexpected error writing cache: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(cacheFile); err != nil {
		t.Errorf("cache file should exist: %v", err)
	}

	// Read cache
	cache, err := cm.ReadCache()
	if err != nil {
		t.Errorf("unexpected error reading cache: %v", err)
	}

	if cache.Version != "v1.0.0" {
		t.Errorf("expected version v1.0.0, got %s", cache.Version)
	}

	if cache.CodeHash != "testhash123" {
		t.Errorf("expected code hash testhash123, got %s", cache.CodeHash)
	}

	if cache.ScanDurationMs != 1000 {
		t.Errorf("expected scan duration 1000ms, got %d", cache.ScanDurationMs)
	}

	if cache.Report.CodeHealthScore == nil || *cache.Report.CodeHealthScore != 8.5 {
		t.Error("report score should be preserved")
	}
}

// TestCacheValidation tests cache validation logic
func TestCacheValidation(t *testing.T) {
	tmpDir := t.TempDir()
	cacheFile := filepath.Join(tmpDir, "cache.json")
	cm := NewCacheManager(cacheFile, "v1.0.0")

	score := 8.5
	report := &Report{
		CodeHealthScore: &score,
		Files:           []FileReport{{Path: "test.go"}},
	}

	// Write cache with hash
	codeHash := "testhash123"
	if err := cm.WriteCache(report, codeHash, 1000); err != nil {
		t.Fatalf("failed to write cache: %v", err)
	}

	// Valid cache should pass validation
	valid, err := cm.ValidateCache(codeHash)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !valid {
		t.Error("cache should be valid with matching hash")
	}

	// Different hash should fail validation
	valid, err = cm.ValidateCache("differenthash")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if valid {
		t.Error("cache should be invalid with different hash")
	}

	// Different version should fail validation
	cm2 := NewCacheManager(cacheFile, "v2.0.0")
	valid, err = cm2.ValidateCache(codeHash)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if valid {
		t.Error("cache should be invalid with different version")
	}
}

// TestCacheReadNonexistent tests reading nonexistent cache
func TestCacheReadNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	cacheFile := filepath.Join(tmpDir, "nonexistent.json")
	cm := NewCacheManager(cacheFile, "v1.0.0")

	_, err := cm.ReadCache()
	if err == nil {
		t.Error("expected error reading nonexistent cache")
	}
}

// TestIsBuildArtifact tests artifact detection
func TestIsBuildArtifact(t *testing.T) {
	tests := []struct {
		path       string
		isArtifact bool
	}{
		{"/path/to/.build/file.go", true},
		{"/path/to/node_modules/file.go", true},
		{"/path/to/.git/file.go", true},
		{"/path/to/.hidden.go", true},
		{"/path/to/normal.go", false},
		{"src/main.go", false},
	}

	for _, tt := range tests {
		result := isBuildArtifact(tt.path)
		if result != tt.isArtifact {
			t.Errorf("path %s: expected %v, got %v", tt.path, tt.isArtifact, result)
		}
	}
}

// TestAtomicCacheWrite tests that cache writes are atomic
func TestAtomicCacheWrite(t *testing.T) {
	tmpDir := t.TempDir()
	cacheFile := filepath.Join(tmpDir, "cache.json")
	cm := NewCacheManager(cacheFile, "v1.0.0")

	score := 8.5
	report := &Report{
		CodeHealthScore: &score,
		Files:           []FileReport{{Path: "test.go"}},
	}

	// Write cache
	if err := cm.WriteCache(report, "hash1", 1000); err != nil {
		t.Fatalf("failed to write cache: %v", err)
	}

	// Verify no temp file remains
	tmpFile := cacheFile + ".tmp"
	if _, err := os.Stat(tmpFile); err == nil {
		t.Error("temporary cache file should not exist after write")
	}

	// Verify cache file exists and is readable
	if _, err := os.Stat(cacheFile); err != nil {
		t.Errorf("cache file should exist: %v", err)
	}

	cache, err := cm.ReadCache()
	if err != nil {
		t.Errorf("cache should be readable: %v", err)
	}

	if cache.CodeHash != "hash1" {
		t.Errorf("expected hash1, got %s", cache.CodeHash)
	}
}
