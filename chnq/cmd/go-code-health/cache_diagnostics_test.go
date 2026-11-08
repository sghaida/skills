package main

import (
	"path/filepath"
	"testing"
)

func TestDecideCacheStatus(t *testing.T) {
	p := newParityProject(t)
	p.seed()
	scan, manifest, _ := p.context()
	previous := p.cacheMgr.ReusableCache()
	if previous == nil {
		t.Fatal("expected reusable cache")
	}

	bypassed := decideCacheStatus(p.cacheMgr, previous, scan, manifest, true)
	if bypassed.Status != cacheStatusBypassed || bypassed.FilesParsed != len(manifest) {
		t.Fatalf("unexpected bypass decision: %+v", bypassed)
	}

	missing := decideCacheStatus(NewCacheManager(filepath.Join(p.root, "absent.json")), nil, scan, manifest, false)
	if missing.Status != cacheStatusMiss {
		t.Fatalf("unexpected miss decision: %+v", missing)
	}

	writeFixture(t, p.root, "broken.json", "{")
	rebuilt := decideCacheStatus(NewCacheManager(filepath.Join(p.root, "broken.json")), nil, scan, manifest, false)
	if rebuilt.Status != cacheStatusRebuilt {
		t.Fatalf("unexpected rebuilt decision: %+v", rebuilt)
	}

	p.write("b/b.go", "package b\n\n// Helper adds four.\nfunc Helper(x int) int { return x + 4 }\n")
	scan, manifest, _ = p.context()
	incremental := decideCacheStatus(p.cacheMgr, previous, scan, manifest, false)
	if incremental.Status != cacheStatusIncremental || incremental.SnapshotsReused != len(manifest)-1 ||
		incremental.FilesParsed != 1 {
		t.Fatalf("unexpected incremental decision: %+v", incremental)
	}

	changed := scan
	changed.ConfigHash = "different"
	invalidated := decideCacheStatus(p.cacheMgr, previous, changed, manifest, false)
	if invalidated.Status != cacheStatusInvalidated {
		t.Fatalf("unexpected invalidated decision: %+v", invalidated)
	}
}

func TestReportCacheDecisionRespectsFlag(_ *testing.T) {
	// Must not panic and must stay silent without --cache-debug.
	reportCacheDecision(RunConfig{}, CacheDecision{Status: cacheStatusHit})
	reportCacheDecision(RunConfig{cacheDebug: true}, CacheDecision{Status: cacheStatusHit, Reason: "test"})
}
