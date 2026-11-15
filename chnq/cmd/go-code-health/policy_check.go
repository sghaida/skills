package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// checkpolicy contains the validated project-wide gate
type checkPolicy struct {
	projectConfig
	Version           int                 `json:"version"`
	MinimumScore      *float64            `json:"minimum_score"`
	MinimumDimensions map[string]*float64 `json:"minimum_dimensions"`
	HealthThresholds  map[string]*float64 `json:"health_thresholds"`
	IncludeTests      bool                `json:"include_tests"`
}

// policycheck records one inclusive threshold comparison
type PolicyCheck struct {
	Name      string   `json:"name"`
	Measured  *float64 `json:"measured"`
	Operator  string   `json:"operator"`
	Threshold float64  `json:"threshold"`
	Status    string   `json:"status"`
}

// policyresult records all checks without truncating failures
type PolicyResult struct {
	Status string        `json:"status"`
	Checks []PolicyCheck `json:"checks"`
}

// nameddimensions pairs existing dimension values with their stable report names
func namedDimensions(d Dimensions) map[string]*float64 {
	names := []string{"complexity", "structure", "duplication", "coupling",
		"cyclic_dependencies", "interface_compliance", "duck_typing"}
	result := make(map[string]*float64, len(names))
	for i, value := range dimensionValues(d) {
		result[names[i]] = value
	}
	return result
}

// healthvalues resolves gate measurements, using package maxima for graph metrics
func healthValues(report Report) map[string]*float64 {
	h := report.Summary.Health
	values := map[string]*float64{
		"minimum_documentation_percent": ptr(h.DocumentationPercent),
		"maximum_ignored_return_values": ptr(float64(h.IgnoredReturnValues)),
		"maximum_panics":                ptr(float64(h.Panics)),
		"maximum_todos":                 ptr(float64(h.TODOs)),
		"maximum_fixmes":                ptr(float64(h.FIXMEs)),
		"maximum_import_cycles":         ptr(float64(h.ImportCycles)),
		"maximum_dependency_depth":      nil,
		"maximum_incoming_dependencies": nil,
		"maximum_git_churn":             nil,
	}
	if report.Configuration.IncludeGitChurn {
		values["maximum_git_churn"] = ptr(float64(h.GitChurn))
	}
	if report.Dimensions.Coupling != nil && len(report.Packages) > 0 {
		depth, incoming := 0, 0
		for _, pkg := range report.Packages {
			depth = max(depth, pkg.DependencyDepth)
			incoming = max(incoming, pkg.IncomingDependencies)
		}
		values["maximum_dependency_depth"] = ptr(float64(depth))
		values["maximum_incoming_dependencies"] = ptr(float64(incoming))
	}
	return values
}

// loadcheckpolicy rejects malformed, missing, or unsupported gate configuration
func loadCheckPolicy(root string) (*checkPolicy, error) {
	raw, present, err := readProjectConfig(root)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, fmt.Errorf("--check requires %s at the project root", projectConfigFileName)
	}
	var policy checkPolicy
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}
	// the legacy parser also validates trailing JSON and scoring settings
	if _, err := parseProjectConfig(raw, filepath.Join(root, projectConfigFileName)); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("parse policy fields: %w", err)
	}
	for _, name := range sortedKeys(fields) {
		if string(fields[name]) == "null" {
			return nil, fmt.Errorf("policy %s must not be null", name)
		}
	}
	if policy.Version != 1 || policy.MinimumScore == nil {
		return nil, fmt.Errorf("policy requires version 1 and minimum_score")
	}
	if err := validateThresholds(map[string]*float64{"minimum_score": policy.MinimumScore},
		map[string]*float64{"minimum_score": nil}, 10, false); err != nil {
		return nil, err
	}
	if err := validateThresholds(policy.MinimumDimensions, namedDimensions(Dimensions{}), 10, false); err != nil {
		return nil, err
	}
	if err := validateThresholds(policy.HealthThresholds, healthValues(Report{}), math.MaxFloat64, true); err != nil {
		return nil, err
	}
	return &policy, nil
}

// validatethresholds checks supported keys, ranges, and count units
func validateThresholds(values, supported map[string]*float64, ceiling float64, counts bool) error {
	for _, name := range sortedKeys(values) {
		value := values[name]
		_, known := supported[name]
		if !known {
			return fmt.Errorf("unsupported policy threshold %q", name)
		}
		if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > ceiling {
			return fmt.Errorf("invalid policy threshold %s", name)
		}
		if name == "minimum_documentation_percent" {
			if *value > 100 {
				return fmt.Errorf("%s must be between 0 and 100", name)
			}
		} else if counts && math.Trunc(*value) != *value {
			return fmt.Errorf("%s must be an integer count", name)
		}
	}
	return nil
}

// evaluatepolicy compares the complete project report, never a filtered view
func evaluatePolicy(report Report, policy *checkPolicy) *PolicyResult {
	result := &PolicyResult{Status: "PASS", Checks: []PolicyCheck{}}
	result.Checks = append(result.Checks, compareThreshold("minimum_score", report.CodeHealthScore, *policy.MinimumScore))
	dimensions := namedDimensions(report.Dimensions)
	for _, name := range sortedKeys(policy.MinimumDimensions) {
		result.Checks = append(result.Checks, compareThreshold("minimum_dimensions."+name,
			dimensions[name], *policy.MinimumDimensions[name]))
	}
	health := healthValues(report)
	for _, name := range sortedKeys(policy.HealthThresholds) {
		result.Checks = append(result.Checks, compareThreshold(name, health[name], *policy.HealthThresholds[name]))
	}
	for _, check := range result.Checks {
		if check.Status != "PASS" {
			result.Status = "FAIL"
		}
	}
	if report.AnalysisStatus != "complete" || report.CodeHealthScore == nil {
		result.Status = "ERROR"
	}
	return result
}

// comparethreshold treats missing and nonfinite measurements as unavailable
func compareThreshold(name string, value *float64, threshold float64) PolicyCheck {
	check := PolicyCheck{Name: name, Measured: value, Threshold: threshold, Status: "PASS", Operator: ">="}
	if strings.HasPrefix(name, "maximum_") {
		check.Operator = "<="
	}
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		check.Measured = nil
		check.Status = "UNAVAILABLE"
	} else if (check.Operator == ">=" && *value < threshold) || (check.Operator == "<=" && *value > threshold) {
		check.Status = "FAIL"
	}
	return check
}

// preparepresentation resolves policy options before cache fingerprinting
func preparePresentation(cfg RunConfig) (RunConfig, error) {
	if cfg.maxFindings < 0 {
		return cfg, fmt.Errorf("--max-findings must be nonnegative")
	}
	if cfg.file != "" && cfg.pkg != "" {
		return cfg, fmt.Errorf("--file and --package are mutually exclusive")
	}
	if !cfg.check && cfg.file == "" && cfg.pkg == "" {
		return cfg, nil
	}
	if cfg.cacheInfo {
		return cfg, fmt.Errorf("--cache-info cannot be combined with --check, --file, or --package")
	}
	root, err := canonicalPath(cfg.path)
	if err != nil {
		return cfg, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return cfg, fmt.Errorf("project root: %w", err)
	}
	if !info.IsDir() {
		return cfg, fmt.Errorf("--check, --file, and --package require a project directory as --path")
	}
	if _, moduleRoot := locateModule(root); moduleRoot != "" && moduleRoot != root {
		return cfg, fmt.Errorf("use module root %s as --path to retain complete-project context", moduleRoot)
	}
	cfg.path = root
	if cfg.check {
		cfg.policy, err = loadCheckPolicy(root)
		if err != nil {
			return cfg, err
		}
		if cfg.includeTestsSet && cfg.includeTests != cfg.policy.IncludeTests {
			return cfg, fmt.Errorf("--include-tests conflicts with project policy")
		}
		cfg.includeTests = cfg.policy.IncludeTests
		if _, required := cfg.policy.HealthThresholds["maximum_git_churn"]; required {
			cfg.includeGitChurn = true
		}
	}
	return cfg, nil
}
