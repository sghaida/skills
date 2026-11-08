package main

import (
	"sort"
)

// IncrementalUpdate manages cache merging and incremental updates
type IncrementalUpdate struct {
	oldCache     *CacheFile
	newReport    *Report
	changedFiles map[string]bool
}

// NewIncrementalUpdate creates an incremental update manager
func NewIncrementalUpdate(oldCache *CacheFile, newReport *Report, changedFiles map[string]bool) *IncrementalUpdate {
	return &IncrementalUpdate{
		oldCache:     oldCache,
		newReport:    newReport,
		changedFiles: changedFiles,
	}
}

// MergeReports merges old and new analysis results
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

	// Merge files: keep old unchanged, add new changed
	fileMap := make(map[string]*FileReport)

	// Add old files (for those not changed)
	if iu.oldCache != nil && iu.oldCache.Report != nil {
		for i := range iu.oldCache.Report.Files {
			f := &iu.oldCache.Report.Files[i]
			if !iu.changedFiles[f.Path] {
				fileMap[f.Path] = f
			}
		}
	}

	// Add/override with new files
	for i := range iu.newReport.Files {
		f := &iu.newReport.Files[i]
		fileMap[f.Path] = f
	}

	// Build merged file list
	for _, f := range fileMap {
		merged.Files = append(merged.Files, *f)
	}

	// Re-sort by score to identify new hotspots
	sort.Slice(merged.Files, func(i, j int) bool {
		if merged.Files[i].Score == merged.Files[j].Score {
			return merged.Files[i].Path < merged.Files[j].Path
		}
		return merged.Files[i].Score < merged.Files[j].Score
	})

	// Recompute hotspots (files with score < 8)
	merged.Hotspots = nil
	for _, f := range merged.Files {
		if f.Score < 8 {
			merged.Hotspots = append(merged.Hotspots, f)
		}
	}

	// Merge packages (recompute from merged files)
	pkgMap := make(map[string]*PackageReport)
	for _, f := range merged.Files {
		if _, exists := pkgMap[f.Package]; !exists {
			pkgMap[f.Package] = &PackageReport{
				Path: f.Package,
			}
		}
		pkg := pkgMap[f.Package]
		pkg.Files++
		pkg.Lines += f.Lines
	}

	for _, pkg := range pkgMap {
		merged.Packages = append(merged.Packages, *pkg)
	}

	// Sort packages by path
	sort.Slice(merged.Packages, func(i, j int) bool {
		return merged.Packages[i].Path < merged.Packages[j].Path
	})

	rebuildAggregateMetrics(merged)
	return merged
}

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
		summary.Health.DependencyDepth += file.Health.DependencyDepth
		summary.Health.IncomingDependencies += file.Health.IncomingDependencies
		summary.Health.GitChurn += file.Health.GitChurn
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
		Complexity:  average(current.Complexity, next.Complexity),
		Structure:   average(current.Structure, next.Structure),
		Duplication: average(current.Duplication, next.Duplication),
		Coupling:    average(current.Coupling, next.Coupling),
	}
}

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

// IdentifyChangedFiles determines which files changed based on hashes
func IdentifyChangedFiles(oldHashes, newHashes map[string]string) map[string]bool {
	changed := make(map[string]bool)

	// Files in new but not old, or with different hash
	for path, newHash := range newHashes {
		if oldHash, exists := oldHashes[path]; !exists || oldHash != newHash {
			changed[path] = true
		}
	}

	// Files in old but not new (deleted)
	for path := range oldHashes {
		if _, exists := newHashes[path]; !exists {
			changed[path] = true
		}
	}

	return changed
}
