package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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

func shouldSkipDirectory(path, root, name string) bool {
	return path != root && (name == "vendor" || name == "testdata" || name == "third_party" || name == "node_modules" || strings.HasPrefix(name, "."))
}

func isGoSource(entry fs.DirEntry) bool {
	return entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".go")
}

func isGoSourcePath(path string) bool {
	return strings.HasSuffix(path, ".go")
}

func isExcludedTest(name string, includeTests bool) bool {
	return strings.HasSuffix(name, "_test.go") && !includeTests
}

func parseSourceFiles(paths []string, root string, summary *Summary, report *Report, workers int) ([]*sourceFile, error) {
	rootDir := moduleSearchRoot(root)
	if workers < 1 {
		workers = 1
	}
	if workers > len(paths) {
		workers = len(paths)
	}

	type result struct {
		index   int
		file    *sourceFile
		ok      bool
		summary Summary
		report  Report
		err     error
	}
	jobs := make(chan int)
	results := make(chan result, len(paths))
	for i := 0; i < workers; i++ {
		go func() {
			for index := range jobs {
				localSummary := Summary{}
				localReport := Report{}
				file, ok, err := parseSourceFile(paths[index], rootDir, &localSummary, &localReport)
				results <- result{index: index, file: file, ok: ok, summary: localSummary, report: localReport, err: err}
			}
		}()
	}
	go func() {
		for i := range paths {
			jobs <- i
		}
		close(jobs)
	}()

	ordered := make([]result, len(paths))
	for range paths {
		item := <-results
		ordered[item.index] = item
	}

	parsedFiles := make([]*sourceFile, 0, len(paths))
	for _, item := range ordered {
		if item.err != nil {
			return nil, item.err
		}
		summary.ExcludedGeneratedFiles += item.summary.ExcludedGeneratedFiles
		report.Warnings = append(report.Warnings, item.report.Warnings...)
		if !item.ok {
			continue
		}
		parsedFiles = append(parsedFiles, item.file)
		summary.GoFiles++
		summary.SourceLines += item.file.lines
		summary.Functions += len(item.file.funcs)
	}
	return parsedFiles, nil
}

func parseSourceFile(path, rootDir string, summary *Summary, report *Report) (*sourceFile, bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		report.Warnings = append(report.Warnings, path+": "+err.Error())
		return nil, false, nil
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
		return nil, false, nil
	}
	return file, true, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
