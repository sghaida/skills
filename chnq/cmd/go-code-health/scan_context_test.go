package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveScanContextDefaults(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "a.go", "package demo\n")
	scan, err := resolveScanContext(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if scan.DuplicationEngine != duplicationEngineInternal || scan.EngineIdentity != duplicationEngineInternal {
		t.Fatalf("expected internal engine, got %+v", scan)
	}
	if scan.BroadInterfaceThreshold != defaultBroadInterfaceThreshold {
		t.Fatalf("expected default threshold, got %d", scan.BroadInterfaceThreshold)
	}
	if scan.ConfigHash != "absent" {
		t.Fatalf("expected absent config hash, got %q", scan.ConfigHash)
	}
	if scan.ReportRoot != scan.Root {
		t.Fatalf("expected report root to equal root for directories")
	}
}

func TestResolveScanContextModuleAndConfig(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/demo\n")
	writeFixture(t, root, "a.go", "package demo\n")
	writeFixture(t, root, projectConfigFileName, `{"broad_interface_threshold": 7}`)
	scan, err := resolveScanContext(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if scan.ModulePath != "example.com/demo" {
		t.Fatalf("expected module path, got %q", scan.ModulePath)
	}
	if scan.BroadInterfaceThreshold != 7 || scan.ConfigHash == "absent" {
		t.Fatalf("expected configured policy, got %+v", scan)
	}
}

func TestResolveScanContextErrors(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, projectConfigFileName, `{"duplication_engine": "invalid"}`)
	if _, err := resolveScanContext(root, false, false); err == nil {
		t.Fatal("expected invalid engine error")
	}
	writeFixture(t, root, projectConfigFileName, "{")
	if _, err := resolveScanContext(root, false, false); err == nil {
		t.Fatal("expected malformed config error")
	}
}

func TestFingerprintChangesWithSemanticInputs(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "a.go", "package demo\n")
	base, err := resolveScanContext(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	variants := []ScanContext{base, base, base, base, base}
	variants[1].IncludeTests = true
	variants[2].IncludeGitChurn = true
	variants[3].ConfigHash = "other"
	variants[4].GitRefsHash = "refs"
	seen := map[string]bool{}
	for i, variant := range variants {
		fingerprint := variant.Fingerprint()
		if i > 0 && seen[fingerprint] {
			t.Fatalf("variant %d did not change the fingerprint", i)
		}
		seen[fingerprint] = true
	}
	if base.Fingerprint() != variants[0].Fingerprint() {
		t.Fatal("fingerprint must be deterministic")
	}
}

func TestCanonicalPath(t *testing.T) {
	root := t.TempDir()
	resolved, err := canonicalPath(root)
	if err != nil || !filepath.IsAbs(resolved) {
		t.Fatalf("canonical path failed: %v %q", err, resolved)
	}
	missing, err := canonicalPath(filepath.Join(root, "missing"))
	if err != nil || !filepath.IsAbs(missing) {
		t.Fatalf("missing path should stay absolute: %v %q", err, missing)
	}
}

func TestResolveEngineIdentityInternal(t *testing.T) {
	identity, err := resolveEngineIdentity(duplicationEngineInternal)
	if err != nil || identity != duplicationEngineInternal {
		t.Fatalf("unexpected internal identity: %v %q", err, identity)
	}
}

func TestResolveEngineIdentityJSCPD(t *testing.T) {
	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "jscpd")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir)
	identity, err := resolveEngineIdentity(duplicationEngineJSCPDRS)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(identity, duplicationEngineJSCPDRS+":") || !strings.Contains(identity, stub) {
		t.Fatalf("unexpected jscpd identity: %q", identity)
	}
}

func TestGitRefsFingerprint(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	if gitRefsFingerprint(root) != "" {
		t.Fatal("expected empty fingerprint outside a repository")
	}
	runGit := func(args ...string) {
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
	}
	runGit("init", "-q")
	empty := gitRefsFingerprint(root)
	writeFixture(t, root, "a.go", "package demo\n")
	runGit("add", "a.go")
	runGit("commit", "-q", "-m", "init")
	committed := gitRefsFingerprint(root)
	if committed == "" || committed == empty {
		t.Fatal("expected history change to change the refs fingerprint")
	}
	if committed != gitRefsFingerprint(root) {
		t.Fatal("expected deterministic refs fingerprint")
	}
}
