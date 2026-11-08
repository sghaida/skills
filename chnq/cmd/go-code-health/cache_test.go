package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testScanContext(t *testing.T, root string) ScanContext {
	t.Helper()
	scan, err := resolveScanContext(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	return scan
}

func testManifest(t *testing.T, scan ScanContext) (map[string]string, string) {
	t.Helper()
	manifest, err := buildSourceManifest(scan)
	if err != nil {
		t.Fatal(err)
	}
	return manifest, manifestFingerprint(manifest)
}

func TestCacheManagerDefaultPath(t *testing.T) {
	if NewCacheManager("").cacheFile != defaultCacheFileName {
		t.Fatal("expected default cache file name")
	}
	if NewCacheManager("custom.json").cacheFile != "custom.json" {
		t.Fatal("expected custom cache file name")
	}
}

func TestFileSHA256(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "a.go", "package demo\n")
	path := filepath.Join(root, "a.go")
	first, err := fileSHA256(path)
	if err != nil || first == "" {
		t.Fatalf("hash failed: %v %q", err, first)
	}
	second, err := fileSHA256(path)
	if err != nil || first != second {
		t.Fatalf("hash not deterministic: %v %q %q", err, first, second)
	}
	if _, err := fileSHA256(filepath.Join(root, "missing.go")); err == nil {
		t.Fatal("expected missing file error")
	}
}

func TestBuildSourceManifest(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "a.go", "package demo\nfunc A() {}\n")
	writeFixture(t, root, "b.go", "package demo\nfunc B() {}\n")
	writeFixture(t, root, "c_test.go", "package demo\n")
	scan := testScanContext(t, root)
	manifest, hash := testManifest(t, scan)
	if len(manifest) != 2 || manifest["a.go"] == "" || manifest["b.go"] == "" {
		t.Fatalf("unexpected manifest: %v", manifest)
	}
	if hash == "" {
		t.Fatal("expected manifest fingerprint")
	}
	scan.IncludeTests = true
	withTests, _ := testManifest(t, scan)
	if len(withTests) != 3 {
		t.Fatalf("expected test file in manifest, got %v", withTests)
	}
}

func TestBuildSourceManifestErrors(t *testing.T) {
	root := t.TempDir()
	scan := testScanContext(t, root)
	scan.Root = filepath.Join(root, "missing")
	if _, err := buildSourceManifest(scan); err == nil {
		t.Fatal("expected missing root error")
	}
	writeFixture(t, root, "a.go", "package demo\n")
	if err := os.Chmod(filepath.Join(root, "a.go"), 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(filepath.Join(root, "a.go"), 0o644) }()
	scan = testScanContext(t, root)
	if _, err := buildSourceManifest(scan); err == nil || !strings.Contains(err.Error(), "hash source file") {
		t.Fatalf("expected fail-closed hash error, got %v", err)
	}
}

func TestManifestFingerprintDetectsRenames(t *testing.T) {
	first := manifestFingerprint(map[string]string{"a.go": "h1", "b.go": "h2"})
	renamed := manifestFingerprint(map[string]string{"z.go": "h1", "b.go": "h2"})
	if first == renamed {
		t.Fatal("rename must change the manifest fingerprint")
	}
	if first != manifestFingerprint(map[string]string{"b.go": "h2", "a.go": "h1"}) {
		t.Fatal("fingerprint must be order independent")
	}
}

func TestWriteAndValidateCache(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "a.go", "package demo\nfunc A() {}\n")
	scan := testScanContext(t, root)
	manifest, manifestHash := testManifest(t, scan)
	manager := NewCacheManager(filepath.Join(root, "cache.json"))
	score := 9.0
	report := Report{CodeHealthScore: &score, AnalysisStatus: "complete"}
	cache := &CacheFile{
		ContextHash:  scan.Fingerprint(),
		ManifestHash: manifestHash,
		FileManifest: manifest,
		Report:       &report,
	}
	if err := manager.WriteCache(cache); err != nil {
		t.Fatal(err)
	}
	stored, err := manager.ReadCache()
	if err != nil || stored.FormatVersion != cacheFormatVersion || stored.ScoringVersion != scoringVersion {
		t.Fatalf("unexpected stored cache: %v %+v", err, stored)
	}
	if manager.ValidCache(scan.Fingerprint(), manifestHash) == nil {
		t.Fatal("expected exact cache hit")
	}
	if manager.ValidCache("other-context", manifestHash) != nil {
		t.Fatal("context mismatch must not hit")
	}
	if manager.ValidCache(scan.Fingerprint(), "other-manifest") != nil {
		t.Fatal("manifest mismatch must not hit")
	}
}

func TestValidCacheRejectsInvalidEntries(t *testing.T) {
	root := t.TempDir()
	manager := NewCacheManager(filepath.Join(root, "cache.json"))
	if manager.ValidCache("ctx", "manifest") != nil {
		t.Fatal("missing cache must not hit")
	}
	writeFixture(t, root, "cache.json", "{")
	if manager.ValidCache("ctx", "manifest") != nil {
		t.Fatal("malformed cache must not hit")
	}
	if _, err := manager.ReadCache(); err == nil {
		t.Fatal("expected malformed cache error")
	}
	if err := manager.WriteCache(&CacheFile{ContextHash: "ctx", ManifestHash: "manifest"}); err != nil {
		t.Fatal(err)
	}
	if manager.ValidCache("ctx", "manifest") != nil {
		t.Fatal("cache without report must not hit")
	}
	stale := &CacheFile{ContextHash: "ctx", ManifestHash: "manifest", Report: &Report{}}
	if err := manager.WriteCache(stale); err != nil {
		t.Fatal(err)
	}
	rewriteCacheField(t, manager.cacheFile, cacheFormatVersion, "1")
	if manager.ValidCache("ctx", "manifest") != nil {
		t.Fatal("old cache format must not hit")
	}
}

func rewriteCacheField(t *testing.T, path, from, to string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replaced := strings.Replace(string(data), `"format_version": "`+from+`"`, `"format_version": "`+to+`"`, 1)
	if err := os.WriteFile(path, []byte(replaced), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAtomicCacheWrite(t *testing.T) {
	root := t.TempDir()
	cachePath := filepath.Join(root, "cache.json")
	manager := NewCacheManager(cachePath)
	if err := manager.WriteCache(&CacheFile{Report: &Report{}}); err != nil {
		t.Fatal(err)
	}
	entries, err := filepath.Glob(cachePath + ".tmp-*")
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary cache files must not remain: %v %v", err, entries)
	}
	missingDir := NewCacheManager(filepath.Join(root, "missing", "cache.json"))
	if err := missingDir.WriteCache(&CacheFile{Report: &Report{}}); err == nil {
		t.Fatal("expected write error for missing directory")
	}
	asDirectory := NewCacheManager(root)
	if err := asDirectory.WriteCache(&CacheFile{Report: &Report{}}); err == nil {
		t.Fatal("expected rename error when destination is a directory")
	}
}

func TestIdentifyChangedFiles(t *testing.T) {
	oldHashes := map[string]string{"same.go": "h1", "changed.go": "h2", "deleted.go": "h3"}
	newHashes := map[string]string{"same.go": "h1", "changed.go": "new", "added.go": "h4"}
	changed := IdentifyChangedFiles(oldHashes, newHashes)
	expected := map[string]bool{"changed.go": true, "deleted.go": true, "added.go": true}
	if len(changed) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, changed)
	}
	for path := range expected {
		if !changed[path] {
			t.Fatalf("expected %s to be marked changed", path)
		}
	}
}
