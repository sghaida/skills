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

// CacheFile represents the cached analysis result
type CacheFile struct {
	Version              string                 `json:"version"`
	ScanTimestamp        string                 `json:"scan_timestamp"`
	CodeHash             string                 `json:"code_hash"`
	FilesHashMap         map[string]string      `json:"files_hash_map"`
	Report               *Report                `json:"report"`
	HotspotsExplanations map[string]interface{} `json:"hotspots_explanations,omitempty"`
	ScanDurationMs       int64                  `json:"scan_duration_ms"`
}

// CacheManager handles cache read/write operations
type CacheManager struct {
	cacheFile string
	version   string
}

// NewCacheManager creates a new cache manager
func NewCacheManager(cacheFile, version string) *CacheManager {
	if cacheFile == "" {
		cacheFile = ".chnq-health-status"
	}
	return &CacheManager{
		cacheFile: cacheFile,
		version:   version,
	}
}

// ComputeRepositoryHash computes SHA256 hash of all .go files in a directory
func (cm *CacheManager) ComputeRepositoryHash(rootPath string) (string, error) {
	h := sha256.New()

	// Walk directory and hash all .go files
	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors, continue walking
		}
		if !info.IsDir() && filepath.Ext(path) == ".go" && !isBuildArtifact(path) {
			file, err := os.Open(path)
			if err != nil {
				return nil // Skip unreadable files
			}
			defer file.Close()
			if _, err := io.Copy(h, file); err != nil {
				return nil
			}
		}
		return nil
	})

	if err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// isBuildArtifact returns true if path should be excluded from hashing
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

// ComputeFileHash computes SHA256 hash of a single file
func (cm *CacheManager) ComputeFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadCache reads the cache file from disk
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

// WriteCache writes the cache file to disk atomically
func (cm *CacheManager) WriteCache(report *Report, codeHash string, scanDurationMs int64, roots ...string) error {
	cache := &CacheFile{
		Version:        cm.version,
		ScanTimestamp:  time.Now().UTC().Format(time.RFC3339),
		CodeHash:       codeHash,
		FilesHashMap:   make(map[string]string),
		Report:         report,
		ScanDurationMs: scanDurationMs,
	}

	// Compute per-file hashes
	for _, file := range report.Files {
		filePath := file.Path
		if len(roots) > 0 && roots[0] != "" {
			filePath = filepath.Join(roots[0], filepath.FromSlash(file.Path))
		}
		hash, err := cm.ComputeFileHash(filePath)
		if err == nil {
			cache.FilesHashMap[file.Path] = hash
		}
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write: write to temp file, then rename
	tmpFile := cm.cacheFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}

	if err := os.Rename(tmpFile, cm.cacheFile); err != nil {
		os.Remove(tmpFile)
		return err
	}

	return nil
}

// ValidateCache checks if cache is valid
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

// ComputeFilesHashMap computes hashes for all files at root path
func (cm *CacheManager) ComputeFilesHashMap(rootPath string, files []string) map[string]string {
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
// paths that reports use, allowing changed files to be rescanned selectively.
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
		hash, hashErr := cm.ComputeFileHash(path)
		if hashErr != nil {
			return nil, hashErr
		}
		relative, relativeErr := filepath.Rel(reportRoot, path)
		if relativeErr != nil {
			return nil, relativeErr
		}
		hashes[filepath.ToSlash(relative)] = hash
	}
	return hashes, nil
}
