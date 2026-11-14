package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// hotspotsexplainer generates detailed explanations for hotspots
type HotspotsExplainer struct {
	report *Report
	scope  string // "file" or "package"
}

// explanation represents a hotspot explanation
type Explanation struct {
	Path                 string
	OverallScore         float64
	MeasuredFindings     string
	WhyScorePoorly       string
	SuggestedImprovement string
	BehaviorToPreserve   string
}

// newhotspotsexplainer creates a new explainer
func NewHotspotsExplainer(report *Report, scope string) *HotspotsExplainer {
	return &HotspotsExplainer{
		report: report,
		scope:  scope,
	}
}

// generateexplanations generates markdown explanations for all hotspots
func (he *HotspotsExplainer) GenerateExplanations() (string, error) {
	var sb strings.Builder
	hotspots := he.scopedHotspots()

	// Write header
	sb.WriteString("## Hotspot Summary\n\n")
	_, _ = fmt.Fprintf(&sb, "Based on the last complete scan, model `%s`.\n\n", he.report.ScoringVersion)

	// Write summary table
	he.writeSummaryTable(&sb, hotspots)

	// Write formula
	sb.WriteString("The weighted score is:\n\n```\nH = 0.40C + 0.25S + 0.20D + 0.15K\n```\n\n")
	sb.WriteString("Where C=Complexity, S=Structure, D=Duplication, K=Coupling\n\n")

	// Write per-hotspot explanations
	for i, hotspot := range hotspots {
		explanation, err := he.explainHotspot(&hotspot)
		if err != nil {
			continue
		}

		_, _ = fmt.Fprintf(&sb, "## %d. %s — %.1f/10\n\n", i+1, strings.TrimSuffix(hotspot.Path, ".go"), hotspot.Score)

		// Measured findings
		sb.WriteString("### Measured findings\n\n")
		sb.WriteString(explanation.MeasuredFindings)
		sb.WriteString("\n\n")

		// Why it scores poorly
		sb.WriteString("### Why it scores poorly\n\n")
		sb.WriteString(explanation.WhyScorePoorly)
		sb.WriteString("\n\n")

		// Suggested improvement
		sb.WriteString("### Suggested improvement\n\n")
		sb.WriteString(explanation.SuggestedImprovement)
		sb.WriteString("\n\n")

		// Behavior to preserve
		sb.WriteString("### Behavior to preserve\n\n")
		sb.WriteString(explanation.BehaviorToPreserve)
		sb.WriteString("\n\n")
	}

	return sb.String(), nil
}

// explainhotspot creates detailed explanation for one hotspot
func (he *HotspotsExplainer) findLongestFunction(hotspot *FileReport) (string, int) {
	maxLen := 0
	maxName := ""
	for _, fn := range hotspot.Functions {
		if fn.Lines > maxLen {
			maxLen = fn.Lines
			maxName = fn.Name
		}
	}
	return maxName, maxLen
}

// buildmeasuredfindings handles build measured findings
func (he *HotspotsExplainer) buildMeasuredFindings(hotspot *FileReport) string {
	var sb strings.Builder
	writeFileFinding(&sb, hotspot)
	writeFunctionFinding(&sb, hotspot)
	writeNestingFinding(&sb, hotspot)
	writeDuplicationFinding(&sb, hotspot)
	writeImportFinding(&sb, hotspot)
	for _, finding := range hotspot.Findings {
		switch finding.Rule {
		case "goto-usage", "undocumented-recursion", "stale-recursion-directive":
			_, _ = fmt.Fprintf(&sb, "- **%s**: %s.\n", finding.Rule, finding.Message)
		}
	}
	return sb.String()
}

// writefilefinding handles write file finding
func writeFileFinding(sb *strings.Builder, hotspot *FileReport) {
	_, _ = fmt.Fprintf(sb, "- **%d-line file**", hotspot.Lines)
	if len(hotspot.Functions) > 0 {
		_, _ = fmt.Fprintf(sb, " with %d functions", len(hotspot.Functions))
	}
	sb.WriteString(".\n")
}

// writefunctionfinding handles write function finding
func writeFunctionFinding(sb *strings.Builder, hotspot *FileReport) {
	maxName, maxLen := (&HotspotsExplainer{}).findLongestFunction(hotspot)
	if maxName == "" {
		return
	}
	_, _ = fmt.Fprintf(sb, "- **%s function**: %d lines", maxName, maxLen)
	for _, fn := range hotspot.Functions {
		if fn.Name != maxName || fn.Lines != maxLen {
			continue
		}
		complexity := maxInt(fn.BranchingComplexity, fn.EffectiveBranchingComplexity)
		if complexity > 0 {
			_, _ = fmt.Fprintf(sb, " with branching complexity of **%d**", complexity)
		}
		break
	}
	sb.WriteString(".\n")
}

// writenestingfinding handles write nesting finding
func writeNestingFinding(sb *strings.Builder, hotspot *FileReport) {
	if len(hotspot.Functions) > 0 && hotspot.Functions[0].MaxNesting > 0 {
		_, _ = fmt.Fprintf(sb, "- Maximum nesting: **%d**.\n", hotspot.Functions[0].MaxNesting)
	}
}

// writeduplicationfinding handles write duplication finding
func writeDuplicationFinding(sb *strings.Builder, hotspot *FileReport) {
	if hotspot.DuplicationPercent > 0 {
		_, _ = fmt.Fprintf(sb, "- **%.1f%% duplicated** tokens: %d of %d.\n",
			hotspot.DuplicationPercent, hotspot.DuplicateTokens, hotspot.TotalFunctionTokens)
	}
}

// writeimportfinding handles write import finding
func writeImportFinding(sb *strings.Builder, hotspot *FileReport) {
	if hotspot.InternalImports > 0 {
		_, _ = fmt.Fprintf(sb, "- Enclosing package imports **%d internal packages**.\n", hotspot.InternalImports)
	}
}

// buildwhyscorepoorly handles build why score poorly
func (he *HotspotsExplainer) buildWhyScorePoorly(hotspot *FileReport) string {
	var sb strings.Builder
	sb.WriteString("The code has multiple concerns:\n\n")

	appendDimensionConcern(&sb, "Complexity", hotspot.Dimensions.Complexity, "Too many branches or long sequential logic")
	appendDimensionConcern(&sb, "Structure", hotspot.Dimensions.Structure, "Function size or deep nesting")
	appendDimensionConcern(&sb, "Duplication", hotspot.Dimensions.Duplication, "Repeated logic patterns")
	appendDimensionConcern(&sb, "Coupling", hotspot.Dimensions.Coupling, "Too many dependencies")

	return sb.String()
}

// appenddimensionconcern handles append dimension concern
func appendDimensionConcern(sb *strings.Builder, name string, score *float64, message string) {
	if score != nil && *score < 5 {
		_, _ = fmt.Fprintf(sb, "- **%s** (%.1f/10): %s\n", name, *score, message)
	}
}

// buildbehaviortopreserve handles build behavior to preserve
func (he *HotspotsExplainer) buildBehaviorToPreserve(path string) string {
	if he.scope == "package" {
		return "- Preserve behavior across the package files represented by this hotspot.\n"
	}
	return he.extractBehaviorPatterns(path)
}

// explainhotspot handles explain hotspot
func (he *HotspotsExplainer) explainHotspot(hotspot *FileReport) (*Explanation, error) {
	exp := &Explanation{
		Path:             hotspot.Path,
		OverallScore:     hotspot.Score,
		MeasuredFindings: he.buildMeasuredFindings(hotspot),
		WhyScorePoorly:   he.buildWhyScorePoorly(hotspot),
		SuggestedImprovement: "Extract focused, testable functions for each concern. " +
			"Separate data transformation from side effects (logging, I/O). " +
			"Reduce nesting by extracting conditional blocks. " +
			"Consolidate repeated patterns into helpers.\n",
		BehaviorToPreserve: he.buildBehaviorToPreserve(hotspot.Path),
	}

	return exp, nil
}

// extractbehaviorpatterns extracts critical behavior from source code
func (he *HotspotsExplainer) extractBehaviorPatterns(filePath string) string {
	file, err := os.Open(filePath)
	if err != nil {
		return "- Unable to extract behavior patterns (file not accessible)\n"
	}
	defer func() { _ = file.Close() }()

	var patterns strings.Builder
	patterns.WriteString("- Error handling: Preserve error types and wrapping\n")
	patterns.WriteString("- Return values: Keep status codes and special cases\n")
	patterns.WriteString("- Cancellation: Preserve context cancellation handling\n")
	patterns.WriteString("- Dependencies: Keep critical imports and their usage\n")

	// Scan for specific patterns (regex)
	scanner := bufio.NewScanner(file)
	patternsFound := make(map[string]bool)

	statusCodeRx := regexp.MustCompile(`(?i)(http\.)?status(code)?.*\d{3}`)
	contextErrRx := regexp.MustCompile(`ctx\.Err\(\)`)
	errorTypeRx := regexp.MustCompile(`errors?\.Is\(|type.*Error`)

	for scanner.Scan() {
		line := scanner.Text()
		appendDetectedPattern(&patterns, patternsFound, line, "status", statusCodeRx,
			"- HTTP status codes: Different 4xx/5xx mappings per error type\n")
		appendDetectedPattern(&patterns, patternsFound, line, "context", contextErrRx,
			"- Context handling: Cancellation signals should be respected\n")
		appendDetectedPattern(&patterns, patternsFound, line, "errors", errorTypeRx,
			"- Error classification: Type assertions and special cases\n")
	}
	if err := scanner.Err(); err != nil {
		patterns.WriteString("- Unable to complete behavior pattern scan (read error)\n")
	}

	return patterns.String()
}

// appenddetectedpattern handles append detected pattern
func appendDetectedPattern(patterns *strings.Builder, found map[string]bool, line, key string, pattern *regexp.Regexp, message string) {
	if pattern.MatchString(line) && !found[key] {
		patterns.WriteString(message)
		found[key] = true
	}
}

// writesummarytable writes the hotspots summary table
func (he *HotspotsExplainer) writeSummaryTable(sb *strings.Builder, hotspots []FileReport) {
	if len(hotspots) == 0 {
		return
	}

	sb.WriteString("| Hotspot | Overall | Complexity | Structure | Duplication | Coupling | Cycles | Interfaces | Duck typing |\n")
	sb.WriteString("|---------|---------|------------|-----------|-------------|----------|--------|------------|-------------|\n")

	for _, h := range hotspots {
		_, _ = fmt.Fprintf(sb, "| %s | **%.1f** | %.1f | %.1f | %.1f | %.1f | %.1f | %.1f | %.1f |\n",
			h.Path,
			h.Score,
			getFloatValue(h.Dimensions.Complexity),
			getFloatValue(h.Dimensions.Structure),
			getFloatValue(h.Dimensions.Duplication),
			getFloatValue(h.Dimensions.Coupling),
			getFloatValue(h.Dimensions.CyclicDependencies),
			getFloatValue(h.Dimensions.InterfaceCompliance),
			getFloatValue(h.Dimensions.DuckTyping),
		)
	}
	sb.WriteString("\n")
}

// scopedhotspots handles scoped hotspots
func (he *HotspotsExplainer) scopedHotspots() []FileReport {
	if he.scope != "package" {
		return he.report.Hotspots
	}

	groups := make(map[string]*hotspotAggregate)
	for _, file := range he.report.Files {
		addHotspotFile(groups, file)
	}

	result := finalizeHotspotGroups(groups)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Score == result[j].Score {
			return result[i].Path < result[j].Path
		}
		return result[i].Score < result[j].Score
	})
	return result
}

// hotspotAggregate stores package-level hotspot data before finalization.
type hotspotAggregate struct {
	report FileReport
	count  int
}

// addHotspotFile adds a low-scoring file to its package aggregate.
func addHotspotFile(groups map[string]*hotspotAggregate, file FileReport) {
	if file.Score >= 8 {
		return
	}
	group := groups[file.Package]
	if group == nil {
		group = &hotspotAggregate{report: FileReport{Path: file.Package, Package: file.Package}}
		groups[file.Package] = group
	}
	group.count++
	group.report.Score += file.Score
	group.report.Lines += file.Lines
	group.report.InternalImports += file.InternalImports
	group.report.DuplicateTokens += file.DuplicateTokens
	group.report.TotalFunctionTokens += file.TotalFunctionTokens
	group.report.Functions = append(group.report.Functions, file.Functions...)
	group.report.Findings = append(group.report.Findings, file.Findings...)
	group.report.Health.IgnoredReturnValues += file.Health.IgnoredReturnValues
	group.report.Health.Panics += file.Health.Panics
	group.report.Health.TODOs += file.Health.TODOs
	group.report.Health.FIXMEs += file.Health.FIXMEs
	group.report.Health.Goroutines += file.Health.Goroutines
	group.report.Health.ChannelOperations += file.Health.ChannelOperations
	group.report.Health.InterfaceCount += file.Health.InterfaceCount
	group.report.Health.UnusedInterfaces += file.Health.UnusedInterfaces
	group.report.Health.BroadInterfaces += file.Health.BroadInterfaces
	group.report.Health.ImplicitImplementations += file.Health.ImplicitImplementations
	group.report.Dimensions = averageDimensions(group.report.Dimensions, file.Dimensions, group.count)
}

// finalizeHotspotGroups converts package aggregates to reports.
func finalizeHotspotGroups(groups map[string]*hotspotAggregate) []FileReport {
	result := make([]FileReport, 0, len(groups))
	for _, group := range groups {
		group.report.Score /= float64(group.count)
		group.report.Classification = classification(group.report.Score)
		if group.report.TotalFunctionTokens > 0 {
			group.report.DuplicationPercent = float64(group.report.DuplicateTokens) * 100 / float64(group.report.TotalFunctionTokens)
		}
		result = append(result, group.report)
	}
	return result
}

// averagedimensions handles average dimensions
func averageDimensions(current, next Dimensions, count int) Dimensions {
	average := func(a, b *float64) *float64 {
		if a == nil {
			return b
		}
		if b == nil {
			return a
		}
		value := (*a*float64(count-1) + *b) / float64(count)
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

// getfloatvalue returns float value or 0 if nil
func getFloatValue(val *float64) float64 {
	if val == nil {
		return 0
	}
	return *val
}
