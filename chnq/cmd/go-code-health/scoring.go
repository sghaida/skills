package main

import (
	"hash/fnv"
	"math"
	"sort"
	"strconv"
)

// clamp limits a score to the tool's 0-to-10 scale.
func clamp(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 10 {
		return 10
	}
	return x
}

// round1 rounds a score or percentage to one decimal place.
func round1(x float64) float64 { return math.Round(x*10) / 10 }

// percent returns n as a percentage of d, or zero when d is zero.
func percent(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return round1(100 * float64(n) / float64(d))
}

// ptr returns a pointer to f for optional report dimensions.
func ptr(f float64) *float64 { return &f }

// maxInt returns the larger of n and m.
func maxInt(n, m int) int {
	if n > m {
		return n
	}
	return m
}

// Each exact 35-token lexical window occurring in *different functions* is treated
// as duplication. The overlap is unioned, so repeated windows do not overcount.
// Comments and automatically inserted semicolons are ignored; names/literals are kept.
type occurrence struct {
	function *functionData
	index    int
}

// tokenHash returns the rolling-hash input for one lexical token.
func tokenHash(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// exactWindow verifies a candidate duplicate after its rolling hashes match.
func exactWindow(a *functionData, ai int, b *functionData, bi, window int) bool {
	for offset := 0; offset < window; offset++ {
		if a.tokens[ai+offset] != b.tokens[bi+offset] {
			return false
		}
	}
	return true
}

// detectDuplicates marks tokens covered by duplicate windows across functions.
func detectDuplicates(files []*sourceFile, window int) {
	const base uint64 = 0x9e3779b185ebca87
	power := uint64(1)
	for i := 1; i < window; i++ {
		power *= base
	}
	first := map[uint64]occurrence{}
	for _, file := range files {
		for _, fn := range file.funcs {
			n := len(fn.tokens)
			fn.duplicateMarkers = make([]int, n+1)
			if n < window {
				continue
			}
			hashes := make([]uint64, n)
			rolling := uint64(0)
			for i, tok := range fn.tokens {
				hashes[i] = tokenHash(tok)
				if i < window {
					rolling = rolling*base + hashes[i]
				}
			}
			for i := 0; i <= n-window; i++ {
				if i > 0 {
					rolling = (rolling-hashes[i-1]*power)*base + hashes[i+window-1]
				}
				if earlier, ok := first[rolling]; ok {
					if earlier.function != fn && exactWindow(earlier.function, earlier.index, fn, i, window) {
						earlier.function.duplicateMarkers[earlier.index]++
						earlier.function.duplicateMarkers[earlier.index+window]--
						fn.duplicateMarkers[i]++
						fn.duplicateMarkers[i+window]--
					}
				} else {
					first[rolling] = occurrence{fn, i}
				}
			}
		}
	}
	for _, f := range files {
		for _, fn := range f.funcs {
			coverage := 0
			for i := range fn.tokens {
				coverage += fn.duplicateMarkers[i]
				if coverage > 0 {
					f.duplicateTokens++
				}
			}
		}
	}
}

// complexityForFunction maps branching complexity to a 0-to-10 score.
func complexityForFunction(v int) float64 {
	switch {
	case v <= 5:
		return 10
	case v <= 10:
		return 8
	case v <= 15:
		return 6
	case v <= 25:
		return 4
	case v <= 40:
		return 2
	default:
		return 0
	}
}

// complexityForFile combines function complexity using a line-weighted average
// and a smaller contribution from the file's worst-scoring function.
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

// structureForFile penalizes large files, long functions, and deep nesting.
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

// couplingForPackage converts internal package fan-out into a 0-to-10 score.
func couplingForPackage(fanOut int) float64 {
	return clamp(10 - 1.25*float64(maxInt(0, fanOut-4)))
}

// scoreDimensions computes the weighted score for available dimensions.
// Missing dimensions are omitted and the remaining weights are normalized.
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
// findings for complexity, size, duplication, coupling, and nesting.
func fileScore(f *sourceFile, moduleAvailable bool) FileReport {
	duplicateTokens := f.duplicateTokens
	totalFunctionTokens := f.totalFunctionTokens
	dup := percent(duplicateTokens, totalFunctionTokens)
	if f.externalDuplicationPercent != nil {
		dup = *f.externalDuplicationPercent
		duplicateTokens = f.externalDuplicateTokens
		totalFunctionTokens = f.externalTotalTokens
	}
	d := Dimensions{
		Complexity:  ptr(round1(complexityForFile(f))),
		Structure:   ptr(round1(structureForFile(f))),
		Duplication: ptr(round1(clamp(10 - float64(dup)/10))),
	}
	if moduleAvailable {
		d.Coupling = ptr(round1(couplingForPackage(f.packageFanOut)))
	}
	score := round1(scoreDimensions(d))
	r := FileReport{
		Path: f.path, Package: f.packageName, Score: score, Classification: classification(score),
		Lines: f.lines, Functions: []FunctionReport{}, InternalImports: f.internalImports,
		DuplicateTokens: duplicateTokens, TotalFunctionTokens: totalFunctionTokens,
		DuplicationPercent: dup, Dimensions: d, Findings: []Finding{},
	}
	for _, fn := range f.funcs {
		r.Functions = append(r.Functions, fn.report)
		if fn.report.BranchingComplexity > 15 {
			severity := "warning"
			if fn.report.BranchingComplexity > 25 {
				severity = "high"
			}
			r.Findings = append(r.Findings, Finding{"high-complexity", severity, fn.report.Line, "Branching complexity " + itoa(fn.report.BranchingComplexity) + " in " + fn.report.Name})
		}
		if fn.report.Lines > 80 {
			r.Findings = append(r.Findings, Finding{"long-function", "warning", fn.report.Line, itoa(fn.report.Lines) + " lines in " + fn.report.Name})
		}
		if fn.report.MaxNesting > 4 {
			r.Findings = append(r.Findings, Finding{"deep-nesting", "warning", fn.report.Line, "Nesting depth " + itoa(fn.report.MaxNesting) + " in " + fn.report.Name})
		}
	}
	if dup >= 10 {
		r.Findings = append(r.Findings, Finding{"duplicate-blocks", "warning", 0, format1(dup) + "% of function tokens match another function"})
	}
	if moduleAvailable && f.packageFanOut > 6 {
		r.Findings = append(r.Findings, Finding{"high-coupling", "warning", 0, "Package imports " + itoa(f.packageFanOut) + " internal packages"})
	}
	if f.lines > 600 {
		r.Findings = append(r.Findings, Finding{"large-file", "warning", 0, itoa(f.lines) + " total lines"})
	}
	sort.Slice(r.Findings, func(i, j int) bool {
		if r.Findings[i].Line == r.Findings[j].Line {
			return r.Findings[i].Rule < r.Findings[j].Rule
		}
		return r.Findings[i].Line < r.Findings[j].Line
	})
	return r
}

// weightedDimensions aggregates file reports using each file's line count as
// its weight, while averaging each dimension only across reports that provide it.
func weightedDimensions(files []FileReport) (Dimensions, float64) {
	sum := [4]float64{}
	sizes := [4]float64{}
	total, totalWeight := 0.0, 0.0
	for _, f := range files {
		weight := float64(maxInt(f.Lines, 1))
		total += f.Score * weight
		totalWeight += weight
		dimVals := []*float64{f.Dimensions.Complexity, f.Dimensions.Structure, f.Dimensions.Duplication, f.Dimensions.Coupling}
		for i, v := range dimVals {
			if v != nil {
				sum[i] += *v * weight
				sizes[i] += weight
			}
		}
	}
	d := Dimensions{}
	dimensions := []*float64{d.Complexity, d.Structure, d.Duplication, d.Coupling}
	for i := range dimensions {
		if sizes[i] > 0 {
			dimensions[i] = ptr(round1(sum[i] / sizes[i]))
		}
	}
	d.Complexity, d.Structure, d.Duplication, d.Coupling = dimensions[0], dimensions[1], dimensions[2], dimensions[3]
	if totalWeight == 0 {
		return d, 0
	}
	return d, round1(total / totalWeight)
}

// buildScores creates deterministic file and package reports and the overall
// repository dimensions and score.
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
		_, score := weightedDimensions(group)
		lines := 0
		for _, f := range group {
			lines += f.Lines
		}
		packageReports = append(packageReports, PackageReport{Path: name, Score: score, Files: len(group), Lines: lines, InternalDependencies: depCounts[name]})
	}
	dims, score := weightedDimensions(results)
	return results, packageReports, dims, ptr(score)
}

// itoa formats an integer for human-readable finding messages.
func itoa(x int) string { return strconv.Itoa(x) }

// format1 formats a floating-point value with one decimal place.
func format1(x float64) string { return strconv.FormatFloat(x, 'f', 1, 64) }
