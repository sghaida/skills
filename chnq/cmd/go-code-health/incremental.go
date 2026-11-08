package main

import (
	"sort"
)

// incrementalupdate manages cache merging and incremental updates
type IncrementalUpdate struct {
	oldCache     *CacheFile
	newReport    *Report
	changedFiles map[string]bool
}

// newincrementalupdate creates an incremental update manager
func NewIncrementalUpdate(oldCache *CacheFile, newReport *Report, changedFiles map[string]bool) *IncrementalUpdate {
	return &IncrementalUpdate{
		oldCache:     oldCache,
		newReport:    newReport,
		changedFiles: changedFiles,
	}
}

// mergefilereports handles merge file reports
func (iu *IncrementalUpdate) mergeFileReports() map[string]*FileReport {
	fileMap := make(map[string]*FileReport)
	iu.addUnchangedFiles(fileMap)
	iu.addNewFiles(fileMap)
	return fileMap
}

// addunchangedfiles handles add unchanged files
func (iu *IncrementalUpdate) addUnchangedFiles(fileMap map[string]*FileReport) {
	if iu.oldCache == nil || iu.oldCache.Report == nil {
		return
	}
	for i := range iu.oldCache.Report.Files {
		f := &iu.oldCache.Report.Files[i]
		if !iu.changedFiles[f.Path] {
			fileMap[f.Path] = f
		}
	}
}

// addnewfiles handles add new files
func (iu *IncrementalUpdate) addNewFiles(fileMap map[string]*FileReport) {
	for i := range iu.newReport.Files {
		f := &iu.newReport.Files[i]
		fileMap[f.Path] = f
	}
}

// buildfilelist handles build file list
func (iu *IncrementalUpdate) buildFileList(fileMap map[string]*FileReport) []FileReport {
	var files []FileReport
	for _, f := range fileMap {
		files = append(files, *f)
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].Score == files[j].Score {
			return files[i].Path < files[j].Path
		}
		return files[i].Score < files[j].Score
	})

	return files
}

// extracthotspots handles extract hotspots
func (iu *IncrementalUpdate) extractHotspots(files []FileReport) []FileReport {
	var hotspots []FileReport
	for _, f := range files {
		if f.Score < 8 {
			hotspots = append(hotspots, f)
		}
	}
	return hotspots
}

// buildpackagereports handles build package reports
func (iu *IncrementalUpdate) buildPackageReports(files []FileReport) []PackageReport {
	pkgMap := make(map[string]*PackageReport)
	pkgFiles := make(map[string][]FileReport)
	for _, f := range files {
		if _, exists := pkgMap[f.Package]; !exists {
			pkgMap[f.Package] = &PackageReport{
				Path: f.Package,
			}
		}
		pkg := pkgMap[f.Package]
		pkg.Files++
		pkg.Lines += f.Lines
		pkgFiles[f.Package] = append(pkgFiles[f.Package], f)
	}

	var packages []PackageReport
	for _, pkg := range pkgMap {
		pkg.Dimensions, pkg.Score = weightedDimensions(pkgFiles[pkg.Path])
		packages = append(packages, *pkg)
	}

	sort.Slice(packages, func(i, j int) bool {
		return packages[i].Path < packages[j].Path
	})

	return packages
}

// mergereports merges old and new analysis results
func (iu *IncrementalUpdate) MergeReports() *Report {
	merged := &Report{
		SchemaVersion:   iu.newReport.SchemaVersion,
		ScoringVersion:  iu.newReport.ScoringVersion,
		Language:        iu.newReport.Language,
		MaxScore:        iu.newReport.MaxScore,
		Configuration:   iu.newReport.Configuration,
		Repository:      iu.newReport.Repository,
		AnalysisStatus:  iu.newReport.AnalysisStatus,
		Summary:         iu.newReport.Summary,
		Dimensions:      iu.newReport.Dimensions,
		CodeHealthScore: iu.newReport.CodeHealthScore,
		Classification:  iu.newReport.Classification,
		Warnings:        iu.newReport.Warnings,
	}

	fileMap := iu.mergeFileReports()
	merged.Files = iu.buildFileList(fileMap)
	merged.Hotspots = iu.extractHotspots(merged.Files)
	merged.Packages = iu.buildPackageReports(merged.Files)

	rebuildAggregateMetrics(merged)
	return merged
}

// accumulatefilemetrics handles accumulate file metrics
func accumulateFileMetrics(summary *Summary, file FileReport) {
	summary.SourceLines += file.Lines
	summary.Functions += len(file.Functions)
	summary.DuplicateTokens += file.DuplicateTokens
	summary.FunctionTokens += file.TotalFunctionTokens
	summary.Health.ExportedSymbols += file.Health.ExportedSymbols
	summary.Health.DocumentedExportedSymbols += file.Health.DocumentedExportedSymbols
	summary.Health.IgnoredReturnValues += file.Health.IgnoredReturnValues
	summary.Health.Panics += file.Health.Panics
	summary.Health.TODOs += file.Health.TODOs
	summary.Health.FIXMEs += file.Health.FIXMEs
	summary.Health.Goroutines += file.Health.Goroutines
	summary.Health.ChannelOperations += file.Health.ChannelOperations
	summary.Health.ImportCycles += file.Health.ImportCycles
	summary.Health.InterfaceCount += file.Health.InterfaceCount
	summary.Health.UnusedInterfaces += file.Health.UnusedInterfaces
	summary.Health.BroadInterfaces += file.Health.BroadInterfaces
	summary.Health.ImplicitImplementations += file.Health.ImplicitImplementations
	summary.Health.DependencyDepth += file.Health.DependencyDepth
	summary.Health.IncomingDependencies += file.Health.IncomingDependencies
	summary.Health.GitChurn += file.Health.GitChurn
}

// rebuildaggregatemetrics handles rebuild aggregate metrics
func rebuildAggregateMetrics(report *Report) {
	summary := report.Summary
	summary.GoFiles = len(report.Files)
	summary.SourceLines = 0
	summary.Functions = 0
	summary.DuplicateTokens = 0
	summary.FunctionTokens = 0
	summary.Health = HealthMetrics{}

	var weighted Dimensions
	var totalWeight float64
	for _, file := range report.Files {
		accumulateFileMetrics(&summary, file)
		weight := float64(file.Lines)
		if weight == 0 {
			weight = 1
		}
		weighted = addWeightedDimensions(weighted, file.Dimensions, weight, totalWeight)
		totalWeight += weight
	}
	summary.DuplicationPercent = percent(summary.DuplicateTokens, summary.FunctionTokens)
	summary.Health.DocumentationPercent = percent(summary.Health.DocumentedExportedSymbols, summary.Health.ExportedSymbols)
	report.Summary = summary
	if totalWeight > 0 {
		report.Dimensions = weighted
		report.CodeHealthScore = aggregateScore(weighted)
		if report.CodeHealthScore != nil {
			report.Classification = classification(*report.CodeHealthScore)
		}
	}
}

// addweighteddimensions handles add weighted dimensions
func addWeightedDimensions(current, next Dimensions, weight, total float64) Dimensions {
	average := func(a, b *float64) *float64 {
		if b == nil {
			return a
		}
		if a == nil || total == 0 {
			value := *b
			return &value
		}
		value := (*a*total + *b*weight) / (total + weight)
		return &value
	}
	return Dimensions{
		Complexity:          average(current.Complexity, next.Complexity),
		Structure:           average(current.Structure, next.Structure),
		Duplication:         average(current.Duplication, next.Duplication),
		Coupling:            average(current.Coupling, next.Coupling),
		CyclicDependencies:  average(current.CyclicDependencies, next.CyclicDependencies),
		InterfaceCompliance: average(current.InterfaceCompliance, next.InterfaceCompliance),
		DuckTyping:          average(current.DuckTyping, next.DuckTyping),
	}
}

// aggregatescore handles aggregate score
func aggregateScore(dimensions Dimensions) *float64 {
	var total, weight float64
	add := func(value *float64, dimensionWeight float64) {
		if value != nil {
			total += *value * dimensionWeight
			weight += dimensionWeight
		}
	}
	add(dimensions.Complexity, 0.40)
	add(dimensions.Structure, 0.25)
	add(dimensions.Duplication, 0.20)
	add(dimensions.Coupling, 0.15)
	if weight == 0 {
		return nil
	}
	score := total / weight
	return &score
}

// identifychangedfiles determines which files changed based on hashes
func IdentifyChangedFiles(oldHashes, newHashes map[string]string) map[string]bool {
	changed := make(map[string]bool)
	addNewOrChangedFiles(changed, oldHashes, newHashes)
	addDeletedFiles(changed, oldHashes, newHashes)
	return changed
}

// addneworchangedfiles handles add new or changed files
func addNewOrChangedFiles(changed map[string]bool, oldHashes, newHashes map[string]string) {
	for path, newHash := range newHashes {
		if oldHash, exists := oldHashes[path]; !exists || oldHash != newHash {
			changed[path] = true
		}
	}
}

// adddeletedfiles handles add deleted files
func addDeletedFiles(changed map[string]bool, oldHashes, newHashes map[string]string) {
	for path := range oldHashes {
		if _, exists := newHashes[path]; !exists {
			changed[path] = true
		}
	}
}
