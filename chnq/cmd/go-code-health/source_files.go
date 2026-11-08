package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// sourcefiles handles source files
func sourceFiles(root string, includeTests bool, sum *Summary) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if !isGoSourcePath(root) {
			return nil, fmt.Errorf("--path must be a directory or .go file")
		}
		return []string{root}, nil
	}
	paths := make([]string, 0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return directoryWalkAction(path, root, entry, includeTests, sum, &paths)
	})
	sort.Strings(paths)
	return paths, err
}

// directorywalkaction handles directory walk action
func directoryWalkAction(path, root string, entry fs.DirEntry, includeTests bool, sum *Summary, paths *[]string) error {
	name := entry.Name()
	if entry.IsDir() {
		if shouldSkipDirectory(path, root, name) {
			return filepath.SkipDir
		}
		return nil
	}
	if !isGoSource(entry) {
		return nil
	}
	if isExcludedTest(name, includeTests) {
		sum.ExcludedTestFiles++
		return nil
	}
	*paths = append(*paths, path)
	return nil
}

// shouldskipdirectory reports whether should skip directory
func shouldSkipDirectory(path, root, name string) bool {
	return path != root && (name == "vendor" || name == "testdata" || name == "third_party" || name == "node_modules" || strings.HasPrefix(name, "."))
}

// isgosource reports whether is go source
func isGoSource(entry fs.DirEntry) bool {
	return entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".go")
}

// isgosourcepath reports whether is go source path
func isGoSourcePath(path string) bool {
	return strings.HasSuffix(path, ".go")
}

// isexcludedtest reports whether is excluded test
func isExcludedTest(name string, includeTests bool) bool {
	return strings.HasSuffix(name, "_test.go") && !includeTests
}

// parseresult represents parse result data
type parseResult struct {
	index   int
	file    *sourceFile
	ok      bool
	summary Summary
	report  Report
	err     error
}

// parsesourcefiles handles parse source files
func parseSourceFiles(paths []string, root string, summary *Summary, report *Report, workers int) ([]*sourceFile, error) {
	rootDir := moduleSearchRoot(root)
	workers = normalizeWorkerCount(workers, len(paths))

	jobs := make(chan int)
	results := make(chan parseResult, len(paths))
	startParseWorkers(paths, rootDir, workers, jobs, results)
	startParseJobs(paths, jobs)
	ordered := collectParseResults(paths, results)

	parsedFiles := make([]*sourceFile, 0, len(paths))
	for _, item := range ordered {
		file, err := addParseResult(item, summary, report)
		if err != nil {
			return nil, err
		}
		if file != nil {
			parsedFiles = append(parsedFiles, file)
		}
	}
	return parsedFiles, nil
}

func normalizeWorkerCount(workers, pathCount int) int {
	if workers < 1 {
		return 1
	}
	if workers > pathCount {
		return pathCount
	}
	return workers
}

func addParseResult(item parseResult, summary *Summary, report *Report) (*sourceFile, error) {
	if item.err != nil {
		return nil, item.err
	}
	summary.ExcludedGeneratedFiles += item.summary.ExcludedGeneratedFiles
	report.Warnings = append(report.Warnings, item.report.Warnings...)
	if !item.ok {
		return nil, nil
	}
	summary.GoFiles++
	summary.SourceLines += item.file.lines
	summary.Functions += len(item.file.funcs)
	return item.file, nil
}

// startparseworkers handles start parse workers
func startParseWorkers(paths []string, rootDir string, workers int, jobs <-chan int, results chan<- parseResult) {
	for i := 0; i < workers; i++ {
		go parseWorker(paths, rootDir, jobs, results)
	}
}

// parseworker handles parse worker
func parseWorker(paths []string, rootDir string, jobs <-chan int, results chan<- parseResult) {
	for index := range jobs {
		localSummary := Summary{}
		localReport := Report{}
		file, ok, err := parseSourceFile(paths[index], rootDir, &localSummary, &localReport)
		results <- parseResult{index: index, file: file, ok: ok, summary: localSummary, report: localReport, err: err}
	}
}

// startparsejobs handles start parse jobs
func startParseJobs(paths []string, jobs chan<- int) {
	go func() {
		for i := range paths {
			jobs <- i
		}
		close(jobs)
	}()
}

// collectparseresults handles collect parse results
func collectParseResults(paths []string, results <-chan parseResult) []parseResult {
	ordered := make([]parseResult, len(paths))
	for range paths {
		item := <-results
		ordered[item.index] = item
	}
	return ordered
}

// parsesourcefile handles parse source file
func parseSourceFile(path, rootDir string, summary *Summary, report *Report) (*sourceFile, bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		report.Warnings = append(report.Warnings, path+": "+err.Error())
		return nil, false, nil //nolint:nilerr // unreadable files are reported and skipped
	}
	if generatedHeader.Match(src[:minInt(len(src), 2048)]) {
		summary.ExcludedGeneratedFiles++
		return nil, false, nil
	}
	relativePath, err := filepath.Rel(rootDir, path)
	if err != nil {
		return nil, false, err
	}
	file, err := scanGo(path, relativePath, src)
	if err != nil {
		report.Warnings = append(report.Warnings, relativePath+": "+err.Error())
		return nil, false, nil //nolint:nilerr // unparsable files are reported and skipped
	}
	return file, true, nil
}

// minint handles min int
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
