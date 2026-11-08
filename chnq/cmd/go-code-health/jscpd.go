package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// JSCPDDuplication is an independent, line-based jscpd-rs result.
// It does not affect the current token-based 0-10 scoring model.
type JSCPDDuplication struct {
	Engine          string       `json:"engine"`
	SourceFiles     int          `json:"source_files"`
	Clones          int          `json:"clones"`
	DuplicatedLines int          `json:"duplicated_lines"`
	DuplicationPct  float64      `json:"duplication_percent"`
	ExampleClones   []JSCPDClone `json:"example_clones"`
}

type JSCPDLocation struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type JSCPDClone struct {
	First  JSCPDLocation `json:"first"`
	Second JSCPDLocation `json:"second"`
	Lines  int           `json:"lines"`
}

// runJSCPD invokes an installed jscpd-rs executable directly from Go.
// No shell is involved. Missing binaries, process failures and invalid
// reports return errors; they must not be mistaken for zero duplication.
func runJSCPD(ctx context.Context, target, binary string, includeTests bool) (*JSCPDDuplication, error) {
	if strings.TrimSpace(binary) == "" {
		return nil, errors.New("jscpd-rs binary path cannot be empty")
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("stat target: %w", err)
	}
	if !info.IsDir() && filepath.Ext(absolute) != ".go" {
		return nil, fmt.Errorf("jscpd target must be a directory or .go file: %s", absolute)
	}
	workdir := absolute
	if !info.IsDir() {
		workdir = filepath.Dir(absolute)
	}
	tempdir, err := os.MkdirTemp("", "go-code-health-jscpd-*")
	if err != nil {
		return nil, fmt.Errorf("create reporter directory: %w", err)
	}
	defer os.RemoveAll(tempdir)

	args := []string{"--format", "go", "--min-lines", "5", "--min-tokens", "50", "--reporters", "json", "--output", tempdir, "--noTips"}
	if !includeTests {
		args = append(args, "--ignore", "**/*_test.go")
	}
	args = append(args, absolute)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = workdir
	output, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("jscpd-rs timed out: %w", ctx.Err())
		}
		if len(output) > 2048 {
			output = output[:2048]
		}
		return nil, fmt.Errorf("jscpd-rs failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	raw, err := os.ReadFile(filepath.Join(tempdir, "jscpd-report.json"))
	if err != nil {
		return nil, fmt.Errorf("read jscpd-rs report: %w", err)
	}
	var report struct {
		Statistics struct {
			Total struct {
				Sources         int     `json:"sources"`
				Clones          int     `json:"clones"`
				DuplicatedLines int     `json:"duplicatedLines"`
				Percentage      float64 `json:"percentage"`
			} `json:"total"`
		} `json:"statistics"`
		Duplicates []struct {
			FirstFile struct {
				Name  string `json:"name"`
				Start int    `json:"start"`
				End   int    `json:"end"`
			} `json:"firstFile"`
			SecondFile struct {
				Name  string `json:"name"`
				Start int    `json:"start"`
				End   int    `json:"end"`
			} `json:"secondFile"`
			Lines int `json:"lines"`
		} `json:"duplicates"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("decode jscpd-rs report: %w", err)
	}
	total := report.Statistics.Total
	if total.Sources < 1 || total.DuplicatedLines < 0 || math.IsNaN(total.Percentage) ||
		math.IsInf(total.Percentage, 0) || total.Percentage < 0 || total.Percentage > 100 {
		return nil, errors.New("invalid or empty jscpd-rs statistics")
	}
	result := &JSCPDDuplication{
		Engine: "jscpd-rs", SourceFiles: total.Sources, Clones: len(report.Duplicates),
		DuplicatedLines: total.DuplicatedLines, DuplicationPct: round1(total.Percentage),
		ExampleClones: make([]JSCPDClone, 0),
	}
	for _, dup := range report.Duplicates {
		if dup.FirstFile.Name == "" || dup.SecondFile.Name == "" {
			continue
		}
		result.ExampleClones = append(result.ExampleClones, JSCPDClone{
			First:  JSCPDLocation{Path: dup.FirstFile.Name, Start: dup.FirstFile.Start, End: dup.FirstFile.End},
			Second: JSCPDLocation{Path: dup.SecondFile.Name, Start: dup.SecondFile.Start, End: dup.SecondFile.End},
			Lines:  dup.Lines,
		})
	}
	sort.Slice(result.ExampleClones, func(i, j int) bool {
		a, b := result.ExampleClones[i], result.ExampleClones[j]
		if a.First.Path != b.First.Path {
			return a.First.Path < b.First.Path
		}
		if a.First.Start != b.First.Start {
			return a.First.Start < b.First.Start
		}
		if a.Second.Path != b.Second.Path {
			return a.Second.Path < b.Second.Path
		}
		return a.Second.Start < b.Second.Start
	})
	if len(result.ExampleClones) > 20 {
		result.ExampleClones = result.ExampleClones[:20]
	}
	return result, nil
}
