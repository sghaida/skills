package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// cacheFormatVersion identifies the serialized cache layout. Caches written
// with any other format are rejected and rebuilt.
const cacheFormatVersion = "2"

// CacheFile is the serialized analysis cache.
type CacheFile struct {
	FormatVersion    string                  `json:"format_version"`
	ScoringVersion   string                  `json:"scoring_version"`
	AnalysisRevision string                  `json:"analysis_revision"`
	ScanTimestamp    string                  `json:"scan_timestamp"`
	ContextHash      string                  `json:"context_hash"`
	ManifestHash     string                  `json:"manifest_hash"`
	FileManifest     map[string]string       `json:"file_manifest"`
	Snapshots        map[string]FileSnapshot `json:"snapshots,omitempty"`
	Report           *Report                 `json:"report"`
	ScanDurationMs   int64                   `json:"scan_duration_ms"`
}

// CacheManager handles cache read and write operations.
type CacheManager struct {
	cacheFile string
}

// NewCacheManager creates a cache manager for the given cache file path.
func NewCacheManager(cacheFile string) *CacheManager {
	if cacheFile == "" {
		cacheFile = defaultCacheFileName
	}
	return &CacheManager{cacheFile: cacheFile}
}

// ReadCache reads and decodes the cache file.
func (cm *CacheManager) ReadCache() (*CacheFile, error) {
	data, err := os.ReadFile(cm.cacheFile)
	if err != nil {
		return nil, err
	}
	var cache CacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	return &cache, nil
}

// ValidCache returns the cached entry only when its format, scoring version,
// scan context, and complete file manifest all match the current scan.
func (cm *CacheManager) ValidCache(contextHash, manifestHash string) *CacheFile {
	cache, err := cm.ReadCache()
	if err != nil || cache.FormatVersion != cacheFormatVersion || cache.ScoringVersion != scoringVersion {
		return nil
	}
	if cache.ContextHash != contextHash || cache.ManifestHash != manifestHash || cache.Report == nil {
		return nil
	}
	return cache
}

// ReusableCache returns the cache when its format, scoring version, and
// analysis revision make its file snapshots reusable. Snapshots hold only
// file-local data, so they stay valid across scan-option changes as long as
// each file's content hash still matches.
func (cm *CacheManager) ReusableCache() *CacheFile {
	cache, err := cm.ReadCache()
	if err != nil || cache.FormatVersion != cacheFormatVersion ||
		cache.ScoringVersion != scoringVersion || cache.AnalysisRevision != analysisRevision {
		return nil
	}
	return cache
}

// WriteCache atomically writes the cache file with restrictive permissions.
func (cm *CacheManager) WriteCache(cache *CacheFile) error {
	cache.FormatVersion = cacheFormatVersion
	cache.ScoringVersion = scoringVersion
	cache.AnalysisRevision = analysisRevision
	cache.ScanTimestamp = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(cm.cacheFile, data)
}

// atomicWriteFile writes data to a unique temporary file in the destination
// directory and renames it into place, so concurrent writers never corrupt
// or interleave the destination file.
func atomicWriteFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	if err := writeAndClose(tmp, data); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// writeAndClose writes data and reports the first write or close failure.
func writeAndClose(file *os.File, data []byte) error {
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// fileSHA256 computes the SHA-256 content hash of one file.
func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// buildSourceManifest enumerates eligible Go files with the same discovery
// rules as the analyzer and hashes their contents. Any unreadable eligible
// file fails the manifest, so a stale cache hit can never hide it.
func buildSourceManifest(scan ScanContext) (map[string]string, error) {
	paths, err := sourceFiles(scan.Root, scan.IncludeTests, &Summary{})
	if err != nil {
		return nil, err
	}
	manifest := make(map[string]string, len(paths))
	for _, path := range paths {
		if err := addManifestEntry(manifest, scan.ReportRoot, path); err != nil {
			return nil, err
		}
	}
	return manifest, nil
}

// addManifestEntry hashes one eligible file under its report-relative path.
func addManifestEntry(manifest map[string]string, reportRoot, path string) error {
	hash, err := fileSHA256(path)
	if err != nil {
		return fmt.Errorf("hash source file: %w", err)
	}
	relative, err := filepath.Rel(reportRoot, path)
	if err != nil {
		return err
	}
	manifest[filepath.ToSlash(relative)] = hash
	return nil
}

// manifestFingerprint hashes the sorted manifest entries, including relative
// paths, so renames and moves invalidate cached reports.
func manifestFingerprint(manifest map[string]string) string {
	paths := make([]string, 0, len(manifest))
	for path := range manifest {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		h.Write([]byte(path))
		h.Write([]byte{0})
		h.Write([]byte(manifest[path]))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// IdentifyChangedFiles reports paths that were added, modified, or deleted
// between two file manifests.
func IdentifyChangedFiles(oldHashes, newHashes map[string]string) map[string]bool {
	changed := make(map[string]bool)
	addNewOrChangedFiles(changed, oldHashes, newHashes)
	addDeletedFiles(changed, oldHashes, newHashes)
	return changed
}

// addNewOrChangedFiles marks paths that are new or have different hashes.
func addNewOrChangedFiles(changed map[string]bool, oldHashes, newHashes map[string]string) {
	for path, newHash := range newHashes {
		if oldHash, exists := oldHashes[path]; !exists || oldHash != newHash {
			changed[path] = true
		}
	}
}

// addDeletedFiles marks paths that no longer exist in the new manifest.
func addDeletedFiles(changed map[string]bool, oldHashes, newHashes map[string]string) {
	for path := range oldHashes {
		if _, exists := newHashes[path]; !exists {
			changed[path] = true
		}
	}
}
