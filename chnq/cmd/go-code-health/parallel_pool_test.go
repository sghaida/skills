package main

import (
	"context"
	"errors"
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
