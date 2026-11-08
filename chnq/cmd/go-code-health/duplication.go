package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	duplicationEngineInternal = "internal"
	duplicationEngineJSCPDRS  = "jscpd-rs"
)

type projectConfig struct {
	DuplicationEngine string `json:"duplication_engine"`
}

type jscpdStatistic struct {
	Tokens           int     `json:"tokens"`
	DuplicatedTokens int     `json:"duplicatedTokens"`
	PercentageTokens float64 `json:"percentageTokens"`
}

type jscpdFormatStatistic struct {
	Sources map[string]jscpdStatistic `json:"sources"`
	Total   jscpdStatistic            `json:"total"`
}

type jscpdReport struct {
	Statistics struct {
		Total   jscpdStatistic                  `json:"total"`
		Formats map[string]jscpdFormatStatistic `json:"formats"`
	} `json:"statistics"`
}

func loadDuplicationEngine(root string) (string, error) {
	configPath := filepath.Join(root, ".go-code-health.json")
	data, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return duplicationEngineInternal, nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", configPath, err)
	}

	var config projectConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("parse %s: %w", configPath, err)
	}
	if config.DuplicationEngine == "" {
		return duplicationEngineInternal, nil
	}
	switch config.DuplicationEngine {
	case duplicationEngineInternal, duplicationEngineJSCPDRS:
		return config.DuplicationEngine, nil
	default:
		return "", fmt.Errorf("invalid duplication_engine %q in %s: expected %q or %q", config.DuplicationEngine, configPath, duplicationEngineInternal, duplicationEngineJSCPDRS)
	}
}

func detectWithJSCPDRS(root string, files []*sourceFile, summary *Summary) error {
	jscpdPath, err := locateJSCPD()
	if err != nil {
		return errors.New("jscpd-rs duplication engine selected, but the jscpd executable was not found; run `make install-jscpd-rs`")
	}

	reportDir, err := os.MkdirTemp("", "go-code-health-jscpd-*")
	if err != nil {
		return fmt.Errorf("create jscpd report directory: %w", err)
	}
	defer os.RemoveAll(reportDir)

	var stderr bytes.Buffer
	cmd := exec.Command(jscpdPath, "--format", "go", "--reporters", "json", "--output", reportDir, root)
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf("run jscpd-rs: %w: %s", err, message)
		}
		return fmt.Errorf("run jscpd-rs: %w", err)
	}

	data, err := os.ReadFile(filepath.Join(reportDir, "jscpd-report.json"))
	if err != nil {
		return fmt.Errorf("read jscpd-rs JSON report: %w", err)
	}
	var report jscpdReport
	if err := json.Unmarshal(data, &report); err != nil {
		return fmt.Errorf("parse jscpd-rs JSON report: %w", err)
	}

	summary.DuplicateTokens = report.Statistics.Total.DuplicatedTokens
	summary.FunctionTokens = report.Statistics.Total.Tokens
	summary.DuplicationPercent = round1(report.Statistics.Total.PercentageTokens)

	for _, format := range report.Statistics.Formats {
		for path, stat := range format.Sources {
			for _, file := range files {
				if sameSourcePath(path, file.path) {
					percent := round1(stat.PercentageTokens)
					file.externalDuplicationPercent = &percent
					file.externalDuplicateTokens = stat.DuplicatedTokens
					file.externalTotalTokens = stat.Tokens
				}
			}
		}
	}
	return nil
}

func locateJSCPD() (string, error) {
	if path, err := exec.LookPath("jscpd"); err == nil {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	candidates := []string{
		filepath.Join(home, ".local", "bin", "jscpd"),
		filepath.Join(home, ".local", "node_modules", ".bin", "jscpd"),
	}
	platformPaths, _ := filepath.Glob(filepath.Join(home, ".local", "node_modules", "jscpd-rs-*", "bin", "jscpd"))
	candidates = append(candidates, platformPaths...)
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

func sameSourcePath(reported, relative string) bool {
	reported = filepath.ToSlash(filepath.Clean(reported))
	relative = filepath.ToSlash(filepath.Clean(relative))
	return reported == relative || strings.HasSuffix(reported, "/"+relative)
}
