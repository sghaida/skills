package main

import (
	"hash/fnv"
	"math"
	"sort"
	"strconv"
)

// clamp limits a score to the tool's 0-to-10 scale
func clamp(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 10 {
		return 10
	}
	return x
}

// round1 rounds a score or percentage to one decimal place
func round1(x float64) float64 { return math.Round(x*10) / 10 }

// percent returns n as a percentage of d, or zero when d is zero
func percent(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return round1(100 * float64(n) / float64(d))
}

// ptr returns a pointer to f for optional report dimensions
func ptr(f float64) *float64 { return &f }

// maxint returns the larger of n and m
func maxInt(n, m int) int {
	if n > m {
		return n
	}
	return m
}

// Each exact 35-token lexical window occurring in *different functions* is treated
// as duplication. The overlap is unioned, so repeated windows do not overcount.
// comments and automatically inserted semicolons are ignored; names/literals are kept
type occurrence struct {
	function *functionData
	index    int
}

// tokenhash returns the rolling-hash input for one lexical token
func tokenHash(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// exactwindow verifies a candidate duplicate after its rolling hashes match
func exactWindow(a *functionData, ai int, b *functionData, bi, window int) bool {
	for offset := 0; offset < window; offset++ {
		if a.tokens[ai+offset] != b.tokens[bi+offset] {
			return false
		}
	}
	return true
}

// detectduplicates marks tokens covered by duplicate windows across functions
func detectDuplicates(files []*sourceFile, window int) {
	const base uint64 = 0x9e3779b185ebca87
	power := rollingPower(base, window)
	first := map[uint64]occurrence{}
	for _, file := range files {
		for _, fn := range file.funcs {
			detectFunctionDuplicates(fn, window, base, power, first)
		}
	}
	for _, f := range files {
		for _, fn := range f.funcs {
			f.duplicateTokens += duplicateCoverage(fn)
		}
	}
}

// rollingpower handles rolling power
func rollingPower(base uint64, window int) uint64 {
	power := uint64(1)
	for i := 1; i < window; i++ {
		power *= base
	}
	return power
}

// detectfunctionduplicates handles detect function duplicates
func detectFunctionDuplicates(fn *functionData, window int, base, power uint64, first map[uint64]occurrence) {
	n := len(fn.tokens)
	fn.duplicateMarkers = make([]int, n+1)
	if n < window {
		return
	}
	hashes := tokenHashes(fn.tokens)
	rolling := initialWindowHash(hashes, window, base)
	for i := 0; i <= n-window; i++ {
		if i > 0 {
			rolling = (rolling-hashes[i-1]*power)*base + hashes[i+window-1]
		}
		recordDuplicateWindow(first, rolling, fn, i, window)
	}
}

// tokenhashes handles token hashes
func tokenHashes(tokens []string) []uint64 {
	hashes := make([]uint64, len(tokens))
	for i, token := range tokens {
		hashes[i] = tokenHash(token)
	}
	return hashes
}

// initialwindowhash handles initial window hash
func initialWindowHash(hashes []uint64, window int, base uint64) uint64 {
	var rolling uint64
	for i := 0; i < window; i++ {
		rolling = rolling*base + hashes[i]
	}
	return rolling
}

// recordduplicatewindow handles record duplicate window
func recordDuplicateWindow(first map[uint64]occurrence, hash uint64, fn *functionData, index, window int) {
	earlier, ok := first[hash]
	if !ok {
		first[hash] = occurrence{fn, index}
		return
	}
	if earlier.function == fn || !exactWindow(earlier.function, earlier.index, fn, index, window) {
		return
	}
	earlier.function.duplicateMarkers[earlier.index]++
	earlier.function.duplicateMarkers[earlier.index+window]--
	fn.duplicateMarkers[index]++
	fn.duplicateMarkers[index+window]--
}

// duplicatecoverage handles duplicate coverage
func duplicateCoverage(fn *functionData) int {
	coverage, duplicateTokens := 0, 0
	for i := range fn.tokens {
		coverage += fn.duplicateMarkers[i]
		if coverage > 0 {
			duplicateTokens++
		}
	}
	return duplicateTokens
}

// complexityforfunction maps branching complexity to a 0-to-10 score
func complexityForFunction(v int) float64 {
	thresholds := []struct {
		max   int
		score float64
	}{
		{5, 10},
		{10, 8},
		{15, 6},
		{25, 4},
		{40, 2},
	}
	for _, threshold := range thresholds {
		if v <= threshold.max {
			return threshold.score
		}
	}
	return 0
}

// complexityForFile combines function complexity using a line-weighted average
// and a smaller contribution from the file's worst-scoring function
func complexityForFile(f *sourceFile) float64 {
	if len(f.funcs) == 0 {
		return 10
	}
	weighted, totalWeight, worst := 0.0, 0.0, 10.0
	for _, fn := range f.funcs {
		score := complexityForFunction(fn.report.BranchingComplexity)
		weight := math.Sqrt(float64(maxInt(1, fn.report.Lines)))
		weighted += weight * score
		totalWeight += weight
		if score < worst {
			worst = score
		}
	}
	return clamp(0.8*weighted/totalWeight + 0.2*worst)
}

// structureforfile penalizes large files, long functions, and deep nesting
func structureForFile(f *sourceFile) float64 {
	sizePenalty := math.Min(2.5, 0.005*float64(maxInt(0, f.lines-400)))
	if len(f.funcs) == 0 {
		return clamp(10 - sizePenalty)
	}
	total, worst := 0.0, 0.0
	for _, fn := range f.funcs {
		p := math.Min(10, 0.08*float64(maxInt(0, fn.report.Lines-40))+1.25*float64(maxInt(0, fn.report.MaxNesting-3)))
		total += p
		if p > worst {
			worst = p
		}
	}
	penalty := 0.6*total/float64(len(f.funcs)) + 0.4*worst + sizePenalty
	return clamp(10 - penalty)
}

// couplingforpackage converts internal package fan-out into a 0-to-10 score
func couplingForPackage(fanOut int) float64 {
	return clamp(10 - 1.25*float64(maxInt(0, fanOut-4)))
}

// scoreDimensions computes the weighted score for available dimensions.
// missing dimensions are omitted and the remaining weights are normalized
func scoreDimensions(d Dimensions) float64 {
	weighted, weights := 0.0, 0.0
	components := []struct {
		value  *float64
		weight float64
	}{
		{d.Complexity, .40}, {d.Structure, .25}, {d.Duplication, .20}, {d.Coupling, .15},
	}
	for _, c := range components {
		if c.value == nil {
			continue
		}
		weighted += *c.value * c.weight
		weights += c.weight
	}
	if weights == 0 {
		return 0
	}
	return clamp(weighted / weights)
}

// fileScore builds a file report from analyzed metrics and emits threshold
// findings for complexity, size, duplication, coupling, and nesting
func fileScore(f *sourceFile, moduleAvailable bool) FileReport {
	duplicateTokens, totalFunctionTokens, dup := duplicationStats(f)
	d := fileDimensions(f, moduleAvailable, dup)
	score := round1(scoreDimensions(d))
	r := FileReport{
		Path: f.path, Package: f.packageName, Score: score, Classification: classification(score),
		Lines: f.lines, Functions: []FunctionReport{}, InternalImports: f.internalImports,
		DuplicateTokens: duplicateTokens, TotalFunctionTokens: totalFunctionTokens,
		DuplicationPercent: dup, Dimensions: d, Health: healthMetricsForFile(f),
		Findings: findingsForFile(f, dup, moduleAvailable),
	}
	for _, fn := range f.funcs {
		r.Functions = append(r.Functions, fn.report)
	}
	sort.Slice(r.Findings, func(i, j int) bool {
		if r.Findings[i].Line == r.Findings[j].Line {
			return r.Findings[i].Rule < r.Findings[j].Rule
		}
		return r.Findings[i].Line < r.Findings[j].Line
	})
	return r
}

// healthmetricsforfile handles health metrics for file
func healthMetricsForFile(f *sourceFile) HealthMetrics {
	return HealthMetrics{
		ExportedSymbols: f.exportedSymbols, DocumentedExportedSymbols: f.documentedExportedSymbols,
		DocumentationPercent: percent(f.documentedExportedSymbols, f.exportedSymbols),
		IgnoredReturnValues:  f.ignoredReturnValues, Panics: f.panics, TODOs: f.todos, FIXMEs: f.fixmes,
		Goroutines: f.goroutines, ChannelOperations: f.channelOperations,
		ImportCycles: f.packageImportCycles, DependencyDepth: f.packageDependencyDepth,
		IncomingDependencies: f.packageFanIn,
		GitChurn:             f.gitChurn,
	}
}

// duplicationstats handles duplication stats
func duplicationStats(f *sourceFile) (int, int, float64) {
	if f.externalDuplicationPercent != nil {
		return f.externalDuplicateTokens, f.externalTotalTokens, *f.externalDuplicationPercent
	}
	duplicateTokens := f.duplicateTokens
	totalFunctionTokens := f.totalFunctionTokens
	return duplicateTokens, totalFunctionTokens, percent(duplicateTokens, totalFunctionTokens)
}

// filedimensions handles file dimensions
func fileDimensions(f *sourceFile, moduleAvailable bool, duplicationPercent float64) Dimensions {
	d := Dimensions{
		Complexity:          ptr(round1(complexityForFile(f))),
		Structure:           ptr(round1(structureForFile(f))),
		Duplication:         ptr(round1(clamp(10 - duplicationPercent/10))),
		CyclicDependencies:  ptr(round1(f.cycleScore)),
		InterfaceCompliance: ptr(round1(f.interfaceScore)),
		DuckTyping:          ptr(round1(f.duckTypingScore)),
	}
	if moduleAvailable {
		d.Coupling = ptr(round1(couplingForPackage(f.packageFanOut)))
	}
	return d
}

// findingsforfile handles findings for file
func findingsForFile(f *sourceFile, duplicationPercent float64, moduleAvailable bool) []Finding {
	findings := findingsForFunctions(f.funcs)
	findings = appendFileFindings(findings, f, duplicationPercent, moduleAvailable)
	return findings
}

// findingsforfunctions handles findings for functions
func findingsForFunctions(functions []*functionData) []Finding {
	findings := make([]Finding, 0)
	for _, fn := range functions {
		findings = append(findings, findingsForFunction(fn)...)
	}
	return findings
}

// appendfilefindings handles append file findings
func appendFileFindings(findings []Finding, f *sourceFile, duplicationPercent float64, moduleAvailable bool) []Finding {
	findings = appendDuplicationFinding(findings, duplicationPercent)
	findings = appendCouplingFinding(findings, f, moduleAvailable)
	findings = appendFileSizeFinding(findings, f)
	findings = appendDocumentationFinding(findings, f)
	findings = appendHealthFindings(findings, f)
	return findings
}

// appendduplicationfinding handles append duplication finding
func appendDuplicationFinding(findings []Finding, duplicationPercent float64) []Finding {
	if duplicationPercent >= 10 {
		findings = append(findings, Finding{"duplicate-blocks", "warning", 0, format1(duplicationPercent) + "% of function tokens match another function"})
	}
	return findings
}

// appendcouplingfinding handles append coupling finding
func appendCouplingFinding(findings []Finding, f *sourceFile, moduleAvailable bool) []Finding {
	if moduleAvailable && f.packageFanOut > 6 {
		findings = append(findings, Finding{"high-coupling", "warning", 0, "Package imports " + itoa(f.packageFanOut) + " internal packages"})
	}
	return findings
}

// appendfilesizefinding handles append file size finding
func appendFileSizeFinding(findings []Finding, f *sourceFile) []Finding {
	if f.lines > 600 {
		findings = append(findings, Finding{"large-file", "warning", 0, itoa(f.lines) + " total lines"})
	}
	return findings
}

// appenddocumentationfinding handles append documentation finding
func appendDocumentationFinding(findings []Finding, f *sourceFile) []Finding {
	if f.exportedSymbols > 0 && f.documentedExportedSymbols < f.exportedSymbols {
		findings = append(findings, Finding{"undocumented-exported-symbols", "warning", 0,
			itoa(f.exportedSymbols-f.documentedExportedSymbols) + " exported symbols lack documentation"})
	}
	return findings
}

// appendhealthfindings handles append health findings
func appendHealthFindings(findings []Finding, f *sourceFile) []Finding {
	if f.ignoredReturnValues > 0 {
		findings = append(findings, Finding{"ignored-return-value", "warning", 0,
			itoa(f.ignoredReturnValues) + " return values are discarded"})
	}
	if f.panics > 0 {
		findings = append(findings, Finding{"panic-usage", "warning", 0, itoa(f.panics) + " panic calls"})
	}
	if f.packageImportCycles > 0 {
		findings = append(findings, Finding{"import-cycle", "high", 0, "Package participates in an internal import cycle"})
	}
	if f.interfaceScore < 5 {
		findings = append(findings, Finding{"interface-compliance", "warning", 0, "Interface usage or breadth is reducing the interface compliance score"})
	}
	if f.duckTypingScore < 5 {
		findings = append(findings, Finding{"duck-typing", "warning", 0, "No matching concrete method set was found for one or more interfaces"})
	}
	if f.gitChurn > 1000 {
		findings = append(findings, Finding{"high-git-churn", "warning", 0, itoa(f.gitChurn) + " changed lines across Git history"})
	}
	return findings
}

// findingsforfunction handles findings for function
func findingsForFunction(fn *functionData) []Finding {
	findings := make([]Finding, 0, 3)
	if fn.report.BranchingComplexity > 15 {
		severity := "warning"
		if fn.report.BranchingComplexity > 25 {
			severity = "high"
		}
		findings = append(findings, Finding{"high-complexity", severity, fn.report.Line, "Branching complexity " + itoa(fn.report.BranchingComplexity) + " in " + fn.report.Name})
	}
	if fn.report.Lines > 80 {
		findings = append(findings, Finding{"long-function", "warning", fn.report.Line, itoa(fn.report.Lines) + " lines in " + fn.report.Name})
	}
	if fn.report.MaxNesting > 4 {
		findings = append(findings, Finding{"deep-nesting", "warning", fn.report.Line, "Nesting depth " + itoa(fn.report.MaxNesting) + " in " + fn.report.Name})
	}
	return findings
}

// weightedDimensions aggregates file reports using each file's line count as
// its weight, while averaging each dimension only across reports that provide it
func weightedDimensions(files []FileReport) (Dimensions, float64) {
	sum, sizes, total, totalWeight := weightedTotals(files)
	return dimensionsFromTotals(sum, sizes, total, totalWeight)
}

// weightedtotals handles weighted totals
func weightedTotals(files []FileReport) ([7]float64, [7]float64, float64, float64) {
	sum := [7]float64{}
	sizes := [7]float64{}
	total, totalWeight := 0.0, 0.0
	for _, f := range files {
		weight := float64(maxInt(f.Lines, 1))
		total += f.Score * weight
		totalWeight += weight
		dimVals := dimensionValues(f.Dimensions)
		for i, v := range dimVals {
			if v != nil {
				sum[i] += *v * weight
				sizes[i] += weight
			}
		}
	}
	return sum, sizes, total, totalWeight
}

// dimensionsfromtotals handles dimensions from totals
func dimensionsFromTotals(sum, sizes [7]float64, total, totalWeight float64) (Dimensions, float64) {
	d := Dimensions{}
	dimensions := dimensionValues(d)
	for i := range dimensions {
		if sizes[i] > 0 {
			dimensions[i] = ptr(round1(sum[i] / sizes[i]))
		}
	}
	d.Complexity, d.Structure, d.Duplication, d.Coupling = dimensions[0], dimensions[1], dimensions[2], dimensions[3]
	d.CyclicDependencies, d.InterfaceCompliance, d.DuckTyping = dimensions[4], dimensions[5], dimensions[6]
	if totalWeight == 0 {
		return d, 0
	}
	return d, round1(total / totalWeight)
}

func dimensionValues(d Dimensions) []*float64 {
	return []*float64{
		d.Complexity, d.Structure, d.Duplication, d.Coupling,
		d.CyclicDependencies, d.InterfaceCompliance, d.DuckTyping,
	}
}

// buildScores creates deterministic file and package reports and the overall
// repository dimensions and score
func buildScores(files []*sourceFile, hasModule bool) ([]FileReport, []PackageReport, Dimensions, *float64) {
	results := make([]FileReport, 0, len(files))
	packages := make(map[string][]FileReport)
	depCounts := make(map[string]int)
	for _, f := range files {
		r := fileScore(f, hasModule)
		results = append(results, r)
		packages[f.packagePath] = append(packages[f.packagePath], r)
		depCounts[f.packagePath] = f.packageFanOut
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Path < results[j].Path })
	names := make([]string, 0, len(packages))
	for name := range packages {
		names = append(names, name)
	}
	sort.Strings(names)
	packageReports := make([]PackageReport, 0, len(names))
	for _, name := range names {
		group := packages[name]
		groupDimensions, score := weightedDimensions(group)
		lines := 0
		var health HealthMetrics
		for _, f := range group {
			lines += f.Lines
			health.ExportedSymbols += f.Health.ExportedSymbols
			health.DocumentedExportedSymbols += f.Health.DocumentedExportedSymbols
			health.IgnoredReturnValues += f.Health.IgnoredReturnValues
			health.Panics += f.Health.Panics
			health.TODOs += f.Health.TODOs
			health.FIXMEs += f.Health.FIXMEs
			health.Goroutines += f.Health.Goroutines
			health.ChannelOperations += f.Health.ChannelOperations
			health.ImportCycles += f.Health.ImportCycles
			health.InterfaceCount += f.Health.InterfaceCount
			health.UnusedInterfaces += f.Health.UnusedInterfaces
			health.BroadInterfaces += f.Health.BroadInterfaces
			health.ImplicitImplementations += f.Health.ImplicitImplementations
			health.IncomingDependencies = f.Health.IncomingDependencies
			health.DependencyDepth = f.Health.DependencyDepth
		}
		health.DocumentationPercent = percent(health.DocumentedExportedSymbols, health.ExportedSymbols)
		packageReports = append(packageReports, PackageReport{
			Path: name, Score: score, Files: len(group), Lines: lines,
			InternalDependencies: depCounts[name], IncomingDependencies: health.IncomingDependencies,
			DependencyDepth: health.DependencyDepth, Dimensions: groupDimensions,
		})
	}
	dims, score := weightedDimensions(results)
	return results, packageReports, dims, ptr(score)
}

// itoa formats an integer for human-readable finding messages
func itoa(x int) string { return strconv.Itoa(x) }

// format1 formats a floating-point value with one decimal place
func format1(x float64) string { return strconv.FormatFloat(x, 'f', 1, 64) }
