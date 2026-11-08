package main

import (
	"bytes"
	"context"
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

// projectconfig represents project config data
type projectConfig struct {
	DuplicationEngine       string `json:"duplication_engine"`
	BroadInterfaceThreshold int    `json:"broad_interface_threshold"`
}

// jscpdstatistic represents jscpd statistic data
type jscpdStatistic struct {
	Tokens           int     `json:"tokens"`
	DuplicatedTokens int     `json:"duplicatedTokens"`
	PercentageTokens float64 `json:"percentageTokens"`
}

// jscpdformatstatistic represents jscpd format statistic data
type jscpdFormatStatistic struct {
	Sources map[string]jscpdStatistic `json:"sources"`
	Total   jscpdStatistic            `json:"total"`
}

// jscpdreport represents jscpd report data
type jscpdReport struct {
	Statistics struct {
		Total   jscpdStatistic                  `json:"total"`
		Formats map[string]jscpdFormatStatistic `json:"formats"`
	} `json:"statistics"`
}

// loadduplicationengine handles load duplication engine
func loadDuplicationEngine(root string) (string, error) {
	config, err := loadProjectConfig(root)
	if err != nil {
		return "", err
	}
	if config.DuplicationEngine == "" {
		return duplicationEngineInternal, nil
	}
	return validateDuplicationEngine(config.DuplicationEngine, filepath.Join(root, ".go-code-health.json"))
}

func loadProjectConfig(root string) (projectConfig, error) {
	configPath := filepath.Join(root, ".go-code-health.json")
	data, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return projectConfig{BroadInterfaceThreshold: defaultBroadInterfaceThreshold}, nil
	}
	if err != nil {
		return projectConfig{}, fmt.Errorf("read %s: %w", configPath, err)
	}

	var config projectConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return projectConfig{}, fmt.Errorf("parse %s: %w", configPath, err)
	}
	if config.BroadInterfaceThreshold < 1 {
		config.BroadInterfaceThreshold = defaultBroadInterfaceThreshold
	}
	return config, nil
}

// validateduplicationengine handles validate duplication engine
func validateDuplicationEngine(engine, configPath string) (string, error) {
	switch engine {
	case duplicationEngineInternal, duplicationEngineJSCPDRS:
		return engine, nil
	default:
		return "", fmt.Errorf("invalid duplication_engine %q in %s: expected %q or %q", engine, configPath, duplicationEngineInternal, duplicationEngineJSCPDRS)
	}
}

// detectwithjscpdrs handles detect with jscpdrs
func detectWithJSCPDRS(root string, files []*sourceFile, summary *Summary) error {
	jscpdPath, err := locateJSCPD()
	if err != nil {
		return errors.New("jscpd-rs duplication engine selected, but the jscpd executable was not found; run `make install-jscpd-rs`")
	}
	reportDir, err := os.MkdirTemp("", "go-code-health-jscpd-*")
	if err != nil {
		return fmt.Errorf("create jscpd report directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(reportDir) }()
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

// runjscpd handles run jscpd
func runJSCPD(path, root, reportDir string) error {
	var stderr bytes.Buffer
	const ignoredPaths = ".github/**,.git/**,**/.build/**,**/vendor/**,**/node_modules/**"
	cmd := exec.CommandContext(context.Background(), path, "--format", "go", "--ignore", ignoredPaths, "--reporters", "json", "--output", reportDir, root)
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

// readjscpdreport handles read jscpdreport
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

// applyjscpdreport handles apply jscpdreport
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

// applysourcestatistic handles apply source statistic
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

// locatejscpd handles locate jscpd
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
	if path := findExecutableCandidate(candidates); path != "" {
		return path, nil
	}
	return "", exec.ErrNotFound
}

// findexecutablecandidate handles find executable candidate
func findExecutableCandidate(candidates []string) string {
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// samesourcepath handles same source path
func sameSourcePath(reported, relative string) bool {
	reported = filepath.ToSlash(filepath.Clean(reported))
	relative = filepath.ToSlash(filepath.Clean(relative))
	return reported == relative || strings.HasSuffix(reported, "/"+relative)
}
