package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// parityProject drives cold, cached, and incremental scans over one fixture
// repository and asserts they produce semantically identical reports.
type parityProject struct {
	t               *testing.T
	root            string
	cacheMgr        *CacheManager
	includeTests    bool
	includeGitChurn bool
}

func newParityProject(t *testing.T) *parityProject {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/parity\n\ngo 1.27.3\n")
	return &parityProject{
		t:        t,
		root:     root,
		cacheMgr: NewCacheManager(filepath.Join(root, "parity-cache.json")),
	}
}

func (p *parityProject) write(name, content string) {
	p.t.Helper()
	writeFixture(p.t, p.root, name, content)
}

func (p *parityProject) remove(name string) {
	p.t.Helper()
	if err := os.Remove(filepath.Join(p.root, name)); err != nil {
		p.t.Fatal(err)
	}
}

func (p *parityProject) context() (ScanContext, map[string]string, string) {
	p.t.Helper()
	scan, err := resolveScanContext(p.root, p.includeTests, p.includeGitChurn)
	if err != nil {
		p.t.Fatal(err)
	}
	if p.includeGitChurn {
		scan.GitRefsHash = gitRefsFingerprint(p.root)
	}
	manifest, err := buildSourceManifest(scan)
	if err != nil {
		p.t.Fatal(err)
	}
	return scan, manifest, manifestFingerprint(manifest)
}

// coldScan runs a full scan without reading or writing the cache.
func (p *parityProject) coldScan() Report {
	p.t.Helper()
	scan, _, _ := p.context()
	report, _, err := analyzeScan(scan, 4)
	if err != nil {
		p.t.Fatal(err)
	}
	return report
}

// scanWithCache mimics the CLI flow: exact hit, otherwise snapshot-reusing
// analysis that refreshes the cache. It reports whether the result was an
// exact cached-report hit.
func (p *parityProject) scanWithCache() (Report, bool) {
	p.t.Helper()
	scan, manifest, manifestHash := p.context()
	if report, found := cachedReport(p.cacheMgr, scan.Fingerprint(), manifestHash, false); found {
		return report, true
	}
	cfg := RunConfig{path: p.root, workers: 4, includeTests: p.includeTests, includeGitChurn: p.includeGitChurn}
	report, err := performAnalysis(p.cacheMgr, scan, cfg, manifest, manifestHash)
	if err != nil {
		p.t.Fatal(err)
	}
	return report, false
}

func canonicalReport(t *testing.T, report Report) string {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (p *parityProject) assertParity(label string) {
	p.t.Helper()
	incremental, _ := p.scanWithCache()
	full := p.coldScan()
	if canonicalReport(p.t, incremental) != canonicalReport(p.t, full) {
		p.t.Fatalf("%s: incremental and full reports differ:\nincremental: %s\nfull: %s",
			label, canonicalReport(p.t, incremental), canonicalReport(p.t, full))
	}
}

const parityShared = `package a

// Shared sums values with branching.
func Shared(values []int) int {
	total := 0
	for _, value := range values {
		if value > 0 {
			total += value
		} else {
			total -= value
		}
		if value%2 == 0 {
			total++
		}
	}
	return total
}
`

func (p *parityProject) seed() {
	p.t.Helper()
	p.write("a/a.go", parityShared)
	p.write("b/b.go", "package b\n\n// Helper adds one.\nfunc Helper(x int) int { return x + 1 }\n")
	if _, hit := p.scanWithCache(); hit {
		p.t.Fatal("first scan must not hit the cache")
	}
}

func TestParityExactHitAfterNoChanges(t *testing.T) {
	p := newParityProject(t)
	p.seed()
	first, _ := p.scanWithCache()
	second, hit := p.scanWithCache()
	if !hit {
		t.Fatal("unchanged repository must produce an exact cache hit")
	}
	if canonicalReport(t, first) != canonicalReport(t, second) {
		t.Fatal("cached report must be semantically identical")
	}
	if canonicalReport(t, first) != canonicalReport(t, p.coldScan()) {
		t.Fatal("cached report must match a cold scan")
	}
}

func TestParityAfterModifyingOneFunction(t *testing.T) {
	p := newParityProject(t)
	p.seed()
	p.write("b/b.go", "package b\n\n// Helper adds two.\nfunc Helper(x int) int { return x + 2 }\n")
	p.assertParity("modify one function")
}

func TestParityAfterCrossFileDuplicate(t *testing.T) {
	p := newParityProject(t)
	p.seed()
	duplicate := "package b\n" + parityShared[len("package a\n"):]
	p.write("b/b.go", duplicate)
	incremental, hit := p.scanWithCache()
	if hit {
		t.Fatal("changed source must not hit the cache")
	}
	for _, file := range incremental.Files {
		if file.DuplicateTokens == 0 {
			t.Fatalf("expected duplicate tokens on both sides, got %+v", file)
		}
	}
	p.assertParity("cross-file duplicate")
}

func TestParityAfterAddAndDelete(t *testing.T) {
	p := newParityProject(t)
	p.seed()
	p.write("c/c.go", "package c\n\n// Extra is new.\nfunc Extra() int { return 3 }\n")
	p.assertParity("add file")
	p.remove("c/c.go")
	p.assertParity("delete file")
	report, _ := p.scanWithCache()
	for _, file := range report.Files {
		if file.Path == "c/c.go" {
			t.Fatal("deleted file must disappear from the report")
		}
	}
}

func TestParityAfterRenameWithoutContentChange(t *testing.T) {
	p := newParityProject(t)
	p.seed()
	if err := os.Rename(filepath.Join(p.root, "b/b.go"), filepath.Join(p.root, "b/renamed.go")); err != nil {
		t.Fatal(err)
	}
	report, hit := p.scanWithCache()
	if hit {
		t.Fatal("a rename must invalidate the exact cache hit")
	}
	found := false
	for _, file := range report.Files {
		found = found || file.Path == "b/renamed.go"
	}
	if !found {
		t.Fatal("renamed file missing from the report")
	}
	p.assertParity("rename file")
}

func TestParityAfterImportChange(t *testing.T) {
	p := newParityProject(t)
	p.seed()
	p.write("b/b.go", `package b

import "example.com/parity/a"

// Helper now depends on package a.
func Helper(values []int) int { return a.Shared(values) }
`)
	incremental, _ := p.scanWithCache()
	coupled := packageByPath(t, incremental, "b")
	if coupled.InternalDependencies != 1 {
		t.Fatalf("expected package b to gain one internal dependency, got %+v", coupled)
	}
	if packageByPath(t, incremental, "a").IncomingDependencies == 0 {
		t.Fatal("unchanged package a must gain incoming dependency metrics")
	}
	p.assertParity("import change")
}

func packageByPath(t *testing.T, report Report, path string) PackageReport {
	t.Helper()
	for _, pkg := range report.Packages {
		if pkg.Path == path {
			return pkg
		}
	}
	t.Fatalf("package %q missing from %+v", path, report.Packages)
	return PackageReport{}
}

func TestParityInterfaceChangeWithUnchangedImplementation(t *testing.T) {
	p := newParityProject(t)
	p.write("d/impl.go", `package d

type thing struct{}

func (t thing) Close() error { return nil }
`)
	p.write("d/iface.go", `package d

// Closer matches thing.
type Closer interface{ Close() error }

var _ = Closer(thing{})
`)
	p.seed()
	p.write("d/iface.go", `package d

// Closer no longer matches thing.
type Closer interface {
	Close() error
	Flush() error
}

var _ Closer
`)
	incremental, _ := p.scanWithCache()
	full := p.coldScan()
	if canonicalReport(t, incremental) != canonicalReport(t, full) {
		t.Fatal("interface change must rescore against unchanged implementations")
	}
}

func TestParityTwoMainPackagesStaySeparate(t *testing.T) {
	p := newParityProject(t)
	p.write("cmd/api/main.go", "package main\n\nfunc main() {}\n")
	p.write("cmd/worker/main.go", "package main\n\nfunc main() {}\n")
	p.seed()
	p.write("cmd/api/main.go", "package main\n\nfunc main() { println(1) }\n")
	report, _ := p.scanWithCache()
	packageByPath(t, report, "cmd/api")
	packageByPath(t, report, "cmd/worker")
	p.assertParity("two main packages")
}

func TestParityOptionAndConfigChangesInvalidate(t *testing.T) {
	p := newParityProject(t)
	p.seed()
	p.write("a/a_test.go", "package a\n\nfunc helperForTests() {}\n")
	if _, hit := p.scanWithCache(); !hit {
		t.Fatal("test-file change must not invalidate a test-excluding scan")
	}
	p.includeTests = true
	if _, hit := p.scanWithCache(); hit {
		t.Fatal("toggling --include-tests must invalidate the cached report")
	}
	p.includeTests = false
	p.write(projectConfigFileName, `{"broad_interface_threshold": 2}`)
	if _, hit := p.scanWithCache(); hit {
		t.Fatal("a policy change must invalidate the cached report")
	}
	p.assertParity("policy change")
}

func TestParityCorruptCacheRebuildsSafely(t *testing.T) {
	p := newParityProject(t)
	p.seed()
	writeFixture(t, p.root, "parity-cache.json", "{corrupt")
	report, hit := p.scanWithCache()
	if hit {
		t.Fatal("corrupt cache must not hit")
	}
	if report.AnalysisStatus != "complete" {
		t.Fatalf("corrupt cache must rebuild safely, got %q", report.AnalysisStatus)
	}
	if _, hit := p.scanWithCache(); !hit {
		t.Fatal("rebuilt cache must serve an exact hit")
	}
}

func TestParityWorkerCountDoesNotChangeResults(t *testing.T) {
	p := newParityProject(t)
	p.seed()
	scan, _, _ := p.context()
	single, _, err := analyzeScanWithCache(scan, 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	many, _, err := analyzeScanWithCache(scan, 32, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalReport(t, single) != canonicalReport(t, many) {
		t.Fatal("worker count must not change the semantic report")
	}
}

func TestParityNoCrossRepositoryCacheReuse(t *testing.T) {
	p := newParityProject(t)
	p.seed()
	other := t.TempDir()
	writeFixture(t, other, "go.mod", "module example.com/parity\n\ngo 1.27.3\n")
	writeFixture(t, other, "a/a.go", parityShared)
	writeFixture(t, other, "b/b.go", "package b\n\n// Helper adds one.\nfunc Helper(x int) int { return x + 1 }\n")
	scan, err := resolveScanContext(other, false, false)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := buildSourceManifest(scan)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := cachedReport(p.cacheMgr, scan.Fingerprint(), manifestFingerprint(manifest), false); found {
		t.Fatal("identical bytes under a different root must not reuse the cache")
	}
}

func TestParityHotspotCapMatchesInBothModes(t *testing.T) {
	p := newParityProject(t)
	complexBody := "\tv := 0\n" + repeatBranches(30) + "\treturn v\n"
	for _, name := range []string{"h1", "h2", "h3", "h4", "h5", "h6", "h7"} {
		p.write(name+"/f.go", "package "+name+"\n\n// F is complex.\nfunc F(x int) int {\n"+complexBody+"}\n")
	}
	p.seed()
	p.write("h1/f.go", "package h1\n\n// F is complex.\nfunc F(x int) int {\n"+complexBody+"\t_ = x\n}\n")
	incremental, _ := p.scanWithCache()
	full := p.coldScan()
	if len(incremental.Hotspots) > 5 || len(incremental.Hotspots) != len(full.Hotspots) {
		t.Fatalf("hotspot cap mismatch: incremental %d, full %d", len(incremental.Hotspots), len(full.Hotspots))
	}
	p.assertParity("hotspot cap")
}

func repeatBranches(n int) string {
	body := ""
	for i := 0; i < n; i++ {
		body += "\tif x > " + itoa(i) + " {\n\t\tv++\n\t}\n"
	}
	return body
}

func TestRestoreUnchangedFilesPartitionsPaths(t *testing.T) {
	p := newParityProject(t)
	p.seed()
	scan, manifest, _ := p.context()
	previous := p.cacheMgr.ReusableCache()
	if previous == nil {
		t.Fatal("expected reusable cache after seeding")
	}
	inputs, err := prepareAnalysisContext(scan)
	if err != nil {
		t.Fatal(err)
	}
	totalPaths := len(inputs.paths)
	restored := restoreUnchangedFiles(&inputs, scan, previous, manifest)
	if len(restored) != totalPaths || len(inputs.paths) != 0 {
		t.Fatalf("expected all %d unchanged files restored without reparsing, restored %d, still parsing %d",
			totalPaths, len(restored), len(inputs.paths))
	}
	if inputs.summary.GoFiles != totalPaths || inputs.summary.SourceLines == 0 {
		t.Fatalf("restored files must contribute summary counters: %+v", inputs.summary)
	}
}

func (p *parityProject) git(args ...string) {
	p.t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", p.root}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if output, err := cmd.CombinedOutput(); err != nil {
		p.t.Fatalf("git %v: %v %s", args, err, output)
	}
}

func TestParityGitChurnScenarios(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	p := newParityProject(t)
	p.includeGitChurn = true
	p.git("init", "-q")
	p.seed()
	p.git("add", ".")
	p.git("commit", "-q", "-m", "init")
	if _, hit := p.scanWithCache(); hit {
		t.Fatal("first churn scan must analyze")
	}
	if _, hit := p.scanWithCache(); !hit {
		t.Fatal("unchanged repository and history must hit the cache")
	}
	p.git("tag", "extra-ref")
	if _, hit := p.scanWithCache(); hit {
		t.Fatal("new Git refs must invalidate churn-dependent cached reports")
	}
	p.write("b/b.go", "package b\n\n// Helper adds three.\nfunc Helper(x int) int { return x + 3 }\n")
	p.git("add", ".")
	p.git("commit", "-q", "-m", "change")
	incremental, _ := p.scanWithCache()
	full := p.coldScan()
	if canonicalReport(t, incremental) != canonicalReport(t, full) {
		t.Fatal("churn-enabled incremental and full reports must agree")
	}
	if len(incremental.GitHotspots) == 0 || incremental.Summary.Health.GitChurn == 0 {
		t.Fatalf("expected git hotspots and churn in incremental report: %+v", incremental.Summary.Health)
	}
}

func TestConcurrentCacheWritesStayValid(t *testing.T) {
	root := t.TempDir()
	manager := NewCacheManager(filepath.Join(root, "cache.json"))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			report := Report{AnalysisStatus: "complete"}
			_ = manager.WriteCache(&CacheFile{ContextHash: itoa(n), ManifestHash: "m", Report: &report})
		}(i)
	}
	wg.Wait()
	cache, err := manager.ReadCache()
	if err != nil || cache.Report == nil || cache.FormatVersion != cacheFormatVersion {
		t.Fatalf("concurrent writes corrupted the cache: %v %+v", err, cache)
	}
}
