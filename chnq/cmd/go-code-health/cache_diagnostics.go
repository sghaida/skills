package main

import (
	"fmt"
	"os"
)

// Cache decision statuses reported by --cache-debug.
const (
	cacheStatusHit         = "HIT"
	cacheStatusIncremental = "INCREMENTAL"
	cacheStatusMiss        = "MISS"
	cacheStatusInvalidated = "INVALIDATED"
	cacheStatusRebuilt     = "REBUILT"
	cacheStatusBypassed    = "BYPASSED"
)

// CacheDecision describes how the cache participated in one scan.
type CacheDecision struct {
	Status          string
	Reason          string
	FilesTotal      int
	SnapshotsReused int
	FilesParsed     int
}

// decideCacheStatus classifies the cache outcome for a scan that was not an
// exact report hit.
func decideCacheStatus(cacheMgr *CacheManager, previous *CacheFile, scan ScanContext,
	manifest map[string]string, noCache bool) CacheDecision {
	total := len(manifest)
	if noCache {
		return CacheDecision{Status: cacheStatusBypassed, Reason: "no-cache-flag", FilesTotal: total, FilesParsed: total}
	}
	if previous == nil {
		return missingOrRebuiltDecision(cacheMgr, total)
	}
	reused := len(reusableSnapshots(previous, manifest))
	decision := CacheDecision{FilesTotal: total, SnapshotsReused: reused, FilesParsed: total - reused}
	if previous.ContextHash != scan.Fingerprint() {
		decision.Status, decision.Reason = cacheStatusInvalidated, "scan-context-changed"
		return decision
	}
	decision.Status, decision.Reason = cacheStatusIncremental, "source-manifest-changed"
	return decision
}

// missingOrRebuiltDecision distinguishes an absent cache from an unreadable
// or obsolete one.
func missingOrRebuiltDecision(cacheMgr *CacheManager, total int) CacheDecision {
	if _, err := cacheMgr.ReadCache(); os.IsNotExist(err) {
		return CacheDecision{Status: cacheStatusMiss, Reason: "no-cache-file", FilesTotal: total, FilesParsed: total}
	}
	return CacheDecision{Status: cacheStatusRebuilt, Reason: "cache-unreadable-or-obsolete", FilesTotal: total, FilesParsed: total}
}

// reportCacheDecision writes diagnostics to stderr when --cache-debug is set,
// keeping stdout a single valid JSON document.
func reportCacheDecision(cfg RunConfig, decision CacheDecision) {
	if !cfg.cacheDebug {
		return
	}
	fmt.Fprintf(os.Stderr, "[chnq cache] status=%s reason=%s\n", decision.Status, decision.Reason)
	fmt.Fprintf(os.Stderr, "[chnq cache] files_total=%d snapshots_reused=%d parsed=%d\n",
		decision.FilesTotal, decision.SnapshotsReused, decision.FilesParsed)
}
