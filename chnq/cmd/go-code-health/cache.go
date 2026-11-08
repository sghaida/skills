package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cachefile represents the cached analysis result
type CacheFile struct {
	Version              string                 `json:"version"`
	ScanTimestamp        string                 `json:"scan_timestamp"`
	CodeHash             string                 `json:"code_hash"`
	FilesHashMap         map[string]string      `json:"files_hash_map"`
	Report               *Report                `json:"report"`
	HotspotsExplanations map[string]interface{} `json:"hotspots_explanations,omitempty"`
	ScanDurationMs       int64                  `json:"scan_duration_ms"`
}

// cachemanager handles cache read/write operations
type CacheManager struct {
	cacheFile string
	version   string
}

// newcachemanager creates a new cache manager
func NewCacheManager(cacheFile, version string) *CacheManager {
	if cacheFile == "" {
		cacheFile = ".chnq-health-status"
	}
	return &CacheManager{
		cacheFile: cacheFile,
		version:   version,
	}
}

// computerepositoryhash computes sha256 hash of all .go files in a directory
func (cm *CacheManager) ComputeRepositoryHash(rootPath string) (string, error) {
	h := sha256.New()

	// Walk directory and hash all .go files
	err := filepath.Walk(rootPath, hashWalkEntry(h))

	if err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashwalkentry reports whether hash walk entry
func hashWalkEntry(hash io.Writer) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".go" || isBuildArtifact(path) {
			return nil
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		defer func() { _ = file.Close() }()
		_, _ = io.Copy(hash, file)
		return nil
	}
}

// isbuildartifact returns true if path should be excluded from hashing
func isBuildArtifact(path string) bool {
	excludeDirs := []string{".build", "node_modules", ".git"}
	for _, dir := range excludeDirs {
		if strings.Contains(path, filepath.Join(string(filepath.Separator), dir, string(filepath.Separator))) ||
			strings.HasPrefix(filepath.Base(path), ".") {
			return true
		}
	}
	return false
}

// computefilehash computes sha256 hash of a single file
func (cm *CacheManager) ComputeFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
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

// readcache reads the cache file from disk
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

// writecache writes the cache file to disk atomically
func (cm *CacheManager) WriteCache(report *Report, codeHash string, scanDurationMs int64, roots ...string) error {
	cache := &CacheFile{
		Version:        cm.version,
		ScanTimestamp:  time.Now().UTC().Format(time.RFC3339),
		CodeHash:       codeHash,
		FilesHashMap:   make(map[string]string),
		Report:         report,
		ScanDurationMs: scanDurationMs,
	}

	cm.populateFileHashes(cache, report.Files, roots)

	// Marshal to JSON
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write: write to temp file, then rename
	tmpFile := cm.cacheFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return err
	}

	if err := os.Rename(tmpFile, cm.cacheFile); err != nil {
		_ = os.Remove(tmpFile)
		return err
	}

	return nil
}

// populatefilehashes handles populate file hashes
func (cm *CacheManager) populateFileHashes(cache *CacheFile, files []FileReport, roots []string) {
	for _, file := range files {
		filePath := file.Path
		if len(roots) > 0 && roots[0] != "" {
			filePath = filepath.Join(roots[0], filepath.FromSlash(file.Path))
		}
		hash, err := cm.ComputeFileHash(filePath)
		if err == nil {
			cache.FilesHashMap[file.Path] = hash
		}
	}
}

// validatecache checks if cache is valid
func (cm *CacheManager) ValidateCache(currentCodeHash string) (bool, error) {
	cache, err := cm.ReadCache()
	if err != nil {
		return false, err
	}

	// Check version
	if cache.Version != cm.version {
		return false, nil
	}

	// Check code hash
	if cache.CodeHash != currentCodeHash {
		return false, nil
	}

	return true, nil
}

// computefileshashmap computes hashes for all files at root path
func (cm *CacheManager) ComputeFilesHashMap(_ string, files []string) map[string]string {
	hashes := make(map[string]string)
	for _, file := range files {
		hash, err := cm.ComputeFileHash(file)
		if err == nil {
			hashes[file] = hash
		}
	}
	return hashes
}

// ComputeRepositoryFileHashes returns per-file hashes using the same relative
// paths that reports use, allowing changed files to be rescanned selectively
func (cm *CacheManager) ComputeRepositoryFileHashes(rootPath string, includeTests bool) (map[string]string, error) {
	root, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}
	paths, err := sourceFiles(root, includeTests, &Summary{})
	if err != nil {
		return nil, err
	}
	reportRoot := moduleSearchRoot(root)
	hashes := make(map[string]string, len(paths))
	for _, path := range paths {
		if err := cm.addRepositoryFileHash(hashes, reportRoot, path); err != nil {
			return nil, err
		}
	}
	return hashes, nil
}

// addrepositoryfilehash handles add repository file hash
func (cm *CacheManager) addRepositoryFileHash(hashes map[string]string, reportRoot, path string) error {
	hash, err := cm.ComputeFileHash(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(reportRoot, path)
	if err != nil {
		return err
	}
	hashes[filepath.ToSlash(relative)] = hash
	return nil
}
