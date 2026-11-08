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
	if err := runJSCPD(jscpdPath, root, reportDir); err != nil {
		return err
	}
	report, err := readJSCPDReport(reportDir)
	if err != nil {
		return err
	}
	applyJSCPDReport(report, files, summary)
	return nil
}

func runJSCPD(path, root, reportDir string) error {
	var stderr bytes.Buffer
	const ignoredPaths = ".github/**,.git/**,**/.build/**,**/vendor/**,**/node_modules/**"
	cmd := exec.Command(path, "--format", "go", "--ignore", ignoredPaths, "--reporters", "json", "--output", reportDir, root)
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf("run jscpd-rs: %w: %s", err, message)
		}
		return fmt.Errorf("run jscpd-rs: %w", err)
	}
	return nil
}

func readJSCPDReport(reportDir string) (jscpdReport, error) {
	data, err := os.ReadFile(filepath.Join(reportDir, "jscpd-report.json"))
	if err != nil {
		return jscpdReport{}, fmt.Errorf("read jscpd-rs JSON report: %w", err)
	}
	var report jscpdReport
	if err := json.Unmarshal(data, &report); err != nil {
		return jscpdReport{}, fmt.Errorf("parse jscpd-rs JSON report: %w", err)
	}
	return report, nil
}

func applyJSCPDReport(report jscpdReport, files []*sourceFile, summary *Summary) {
	summary.DuplicateTokens = report.Statistics.Total.DuplicatedTokens
	summary.FunctionTokens = report.Statistics.Total.Tokens
	summary.DuplicationPercent = round1(report.Statistics.Total.PercentageTokens)
	for _, format := range report.Statistics.Formats {
		for path, stat := range format.Sources {
			applySourceStatistic(path, stat, files)
		}
	}
}

func applySourceStatistic(path string, stat jscpdStatistic, files []*sourceFile) {
	for _, file := range files {
		if !sameSourcePath(path, file.path) {
			continue
		}
		percent := round1(stat.PercentageTokens)
		file.externalDuplicationPercent = &percent
		file.externalDuplicateTokens = stat.DuplicatedTokens
		file.externalTotalTokens = stat.Tokens
	}
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
