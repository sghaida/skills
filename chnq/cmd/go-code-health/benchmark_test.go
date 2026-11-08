package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func benchmarkProject(b *testing.B) string {
	b.Helper()
	root := b.TempDir()
	writeBenchmarkFixture(b, root, "go.mod", "module example.com/benchmark\n\ngo 1.27.3\n")
	for i := 0; i < 32; i++ {
		writeBenchmarkFixture(b, root, filepath.Join("pkg", fmt.Sprintf("file_%02d.go", i)),
			fmt.Sprintf("package pkg\n\nfunc Function%02d(value int) int {\n\tif value < 0 {\n\t\treturn -value\n\t}\n\treturn value + %d\n}\n", i, i))
	}
	return root
}

func writeBenchmarkFixture(b *testing.B, root, name, content string) {
	b.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		b.Fatal(err)
	}
}

func benchmarkConfig(root string, cacheFile string, noCache bool) RunConfig {
	return RunConfig{
		path:          root,
		output:        "json",
		workers:       10,
		noCache:       noCache,
		cacheFile:     cacheFile,
		hotspotsScope: "file",
	}
}

func analyzeForBenchmark(cacheManager *CacheManager, config RunConfig) error {
	scan, err := resolveScanContext(config.path, config.includeTests, config.includeGitChurn)
	if err != nil {
		return err
	}
	manifest, err := buildSourceManifest(scan)
	if err != nil {
		return err
	}
	_, err = performAnalysis(cacheManager, scan, config, manifest, manifestFingerprint(manifest))
	return err
}

func BenchmarkColdScan(b *testing.B) {
	root := benchmarkProject(b)
	cacheFile := filepath.Join(root, "cold-cache.json")
	cacheManager := NewCacheManager(cacheFile)
	config := benchmarkConfig(root, cacheFile, true)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := analyzeForBenchmark(cacheManager, config); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCachedScan(b *testing.B) {
	root := benchmarkProject(b)
	cacheFile := filepath.Join(root, "cached-cache.json")
	cacheManager := NewCacheManager(cacheFile)
	config := benchmarkConfig(root, cacheFile, false)
	if err := analyzeForBenchmark(cacheManager, config); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scan, err := resolveScanContext(config.path, false, false)
		if err != nil {
			b.Fatal(err)
		}
		manifest, err := buildSourceManifest(scan)
		if err != nil {
			b.Fatal(err)
		}
		if _, found := cachedReport(cacheManager, scan.Fingerprint(), manifestFingerprint(manifest), false); !found {
			b.Fatal("expected a valid cached report")
		}
	}
}

func BenchmarkIncrementalScan(b *testing.B) {
	root := benchmarkProject(b)
	cacheFile := filepath.Join(root, "incremental-cache.json")
	cacheManager := NewCacheManager(cacheFile)
	config := benchmarkConfig(root, cacheFile, false)
	if err := analyzeForBenchmark(cacheManager, config); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		writeBenchmarkFixture(b, root, filepath.Join("pkg", "file_00.go"),
			fmt.Sprintf("package pkg\n\nfunc Function00(value int) int {\n\treturn value + %d\n}\n", i))
		if err := analyzeForBenchmark(cacheManager, config); err != nil {
			b.Fatal(err)
		}
	}
}
