package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
)

// analysisRevision invalidates cached reports when analysis logic changes
// measured results without a scoring-model version bump.
const analysisRevision = "1"

// defaultCacheFileName is resolved against the analyzed project root when the
// --cache-file flag is left at its default value.
const defaultCacheFileName = ".chnq-health-status"

// ScanContext is the immutable resolved configuration for one analyzer run.
// Every field that can change measured results participates in Fingerprint.
type ScanContext struct {
	Root                    string
	ReportRoot              string
	ModulePath              string
	ModuleRoot              string
	IncludeTests            bool
	IncludeGitChurn         bool
	DuplicationEngine       string
	EngineIdentity          string
	BroadInterfaceThreshold int
	ConfigHash              string
	GitRefsHash             string
}

// resolveScanContext canonicalizes the target path and resolves all
// result-affecting analysis options from the project policy file.
func resolveScanContext(path string, includeTests, includeGitChurn bool) (ScanContext, error) {
	root, err := canonicalPath(path)
	if err != nil {
		return ScanContext{}, fmt.Errorf("resolve --path: %w", err)
	}
	scan := ScanContext{
		Root:            root,
		ReportRoot:      moduleSearchRoot(root),
		IncludeTests:    includeTests,
		IncludeGitChurn: includeGitChurn,
	}
	if err := resolveScanPolicy(&scan); err != nil {
		return ScanContext{}, err
	}
	scan.ModulePath, scan.ModuleRoot = locateModule(root)
	return scan, nil
}

// resolveScanPolicy loads the project policy file and resolves the
// duplication engine together with its external engine identity.
func resolveScanPolicy(scan *ScanContext) error {
	configPath := filepath.Join(scan.ReportRoot, projectConfigFileName)
	raw, present, err := readProjectConfig(scan.ReportRoot)
	if err != nil {
		return err
	}
	config, err := parseProjectConfig(raw, configPath)
	if err != nil {
		return err
	}
	engine := config.DuplicationEngine
	if engine == "" {
		engine = duplicationEngineInternal
	}
	if _, err := validateDuplicationEngine(engine, configPath); err != nil {
		return err
	}
	scan.DuplicationEngine = engine
	scan.BroadInterfaceThreshold = config.BroadInterfaceThreshold
	scan.ConfigHash = configFingerprint(raw, present)
	scan.EngineIdentity, err = resolveEngineIdentity(engine)
	return err
}

// canonicalPath returns the absolute, symlink-resolved form of path. A path
// that does not exist yet stays absolute so discovery reports the real error.
func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs, nil //nolint:nilerr // missing paths fail later with a clearer discovery error
	}
	return resolved, nil
}

// configFingerprint hashes the raw project policy bytes so any policy change
// conservatively invalidates cached reports.
func configFingerprint(raw []byte, present bool) string {
	if !present {
		return "absent"
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// resolveEngineIdentity returns a stable identity for the selected
// duplication engine. For jscpd-rs it includes the resolved executable path
// and its content hash so a replaced binary invalidates cached reports.
func resolveEngineIdentity(engine string) (string, error) {
	if engine != duplicationEngineJSCPDRS {
		return engine, nil
	}
	path, err := locateJSCPD()
	if err != nil {
		return "", errMissingJSCPD()
	}
	hash, err := fileSHA256(path)
	if err != nil {
		return "", fmt.Errorf("fingerprint jscpd executable: %w", err)
	}
	return engine + ":" + path + ":" + hash, nil
}

// Fingerprint returns the semantic cache fingerprint for this scan context.
// Output-only options such as --format and --workers are deliberately absent.
func (c ScanContext) Fingerprint() string {
	h := sha256.New()
	for _, field := range []string{
		"format:" + cacheFormatVersion,
		"scoring:" + scoringVersion,
		"analysis:" + analysisRevision,
		"root:" + c.Root,
		"module:" + c.ModulePath,
		"module_root:" + c.ModuleRoot,
		"include_tests:" + strconv.FormatBool(c.IncludeTests),
		"include_git_churn:" + strconv.FormatBool(c.IncludeGitChurn),
		"engine:" + c.DuplicationEngine,
		"engine_identity:" + c.EngineIdentity,
		"broad_interface_threshold:" + strconv.Itoa(c.BroadInterfaceThreshold),
		"config:" + c.ConfigHash,
		"git_refs:" + c.GitRefsHash,
	} {
		h.Write([]byte(field))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// gitRefsFingerprint hashes all Git refs and HEAD so history changes
// invalidate churn-dependent cached reports. It returns an empty fingerprint
// when the history is unreadable; churn analysis then reports the real error.
func gitRefsFingerprint(root string) string {
	refs, err := exec.CommandContext(context.Background(),
		"git", "-C", root, "for-each-ref", "--format=%(objectname) %(refname)").Output()
	if err != nil {
		return ""
	}
	head, _ := exec.CommandContext(context.Background(), "git", "-C", root, "rev-parse", "HEAD").Output()
	sum := sha256.Sum256(append(refs, head...))
	return hex.EncodeToString(sum[:])
}
