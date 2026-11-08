package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// HotspotsExplainer generates detailed explanations for hotspots
type HotspotsExplainer struct {
	report *Report
	scope  string // "file" or "package"
}

// Explanation represents a hotspot explanation
type Explanation struct {
	Path                 string
	OverallScore         float64
	MeasuredFindings     string
	WhyScorePoorly       string
	SuggestedImprovement string
	BehaviorToPreserve   string
}

// NewHotspotsExplainer creates a new explainer
func NewHotspotsExplainer(report *Report, scope string) *HotspotsExplainer {
	return &HotspotsExplainer{
		report: report,
		scope:  scope,
	}
}

// GenerateExplanations generates markdown explanations for all hotspots
func (he *HotspotsExplainer) GenerateExplanations() (string, error) {
	var sb strings.Builder
	hotspots := he.scopedHotspots()

	// Write header
	sb.WriteString("## Hotspot Summary\n\n")
	sb.WriteString(fmt.Sprintf("Based on the last complete scan, model `%s`.\n\n", he.report.ScoringVersion))

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

		sb.WriteString(fmt.Sprintf("## %d. %s — %.1f/10\n\n", i+1, strings.TrimSuffix(hotspot.Path, ".go"), hotspot.Score))

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

// explainHotspot creates detailed explanation for one hotspot
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

func (he *HotspotsExplainer) buildMeasuredFindings(hotspot *FileReport) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("- **%d-line file**", hotspot.Lines))
	if len(hotspot.Functions) > 0 {
		sb.WriteString(fmt.Sprintf(" with %d functions", len(hotspot.Functions)))
	}
	sb.WriteString(".\n")

	maxName, maxLen := he.findLongestFunction(hotspot)
	if maxName != "" {
		sb.WriteString(fmt.Sprintf("- **%s function**: %d lines", maxName, maxLen))
		if len(hotspot.Functions) > 0 && hotspot.Functions[0].BranchingComplexity > 0 {
			sb.WriteString(fmt.Sprintf(" with branching complexity of **%d**", hotspot.Functions[0].BranchingComplexity))
		}
		sb.WriteString(".\n")
	}

	if len(hotspot.Functions) > 0 && hotspot.Functions[0].MaxNesting > 0 {
		sb.WriteString(fmt.Sprintf("- Maximum nesting: **%d**.\n", hotspot.Functions[0].MaxNesting))
	}

	if hotspot.DuplicationPercent > 0 {
		sb.WriteString(fmt.Sprintf("- **%.1f%% duplicated** tokens: %d of %d.\n",
			hotspot.DuplicationPercent, hotspot.DuplicateTokens, hotspot.TotalFunctionTokens))
	}

	if hotspot.InternalImports > 0 {
		sb.WriteString(fmt.Sprintf("- Enclosing package imports **%d internal packages**.\n", hotspot.InternalImports))
	}

	return sb.String()
}

func (he *HotspotsExplainer) buildWhyScorePoorly(hotspot *FileReport) string {
	var sb strings.Builder
	sb.WriteString("The code has multiple concerns:\n\n")

	complexity := hotspot.Dimensions.Complexity
	structure := hotspot.Dimensions.Structure
	duplication := hotspot.Dimensions.Duplication
	coupling := hotspot.Dimensions.Coupling

	if complexity != nil && *complexity < 5 {
		sb.WriteString(fmt.Sprintf("- **Complexity** (%.1f/10): Too many branches or long sequential logic\n", *complexity))
	}
	if structure != nil && *structure < 5 {
		sb.WriteString(fmt.Sprintf("- **Structure** (%.1f/10): Function size or deep nesting\n", *structure))
	}
	if duplication != nil && *duplication < 5 {
		sb.WriteString(fmt.Sprintf("- **Duplication** (%.1f/10): Repeated logic patterns\n", *duplication))
	}
	if coupling != nil && *coupling < 5 {
		sb.WriteString(fmt.Sprintf("- **Coupling** (%.1f/10): Too many dependencies\n", *coupling))
	}

	return sb.String()
}

func (he *HotspotsExplainer) buildBehaviorToPreserve(path string) string {
	if he.scope == "package" {
		return "- Preserve behavior across the package files represented by this hotspot.\n"
	}
	return he.extractBehaviorPatterns(path)
}

func (he *HotspotsExplainer) explainHotspot(hotspot *FileReport) (*Explanation, error) {
	exp := &Explanation{
		Path:                 hotspot.Path,
		OverallScore:         hotspot.Score,
		MeasuredFindings:     he.buildMeasuredFindings(hotspot),
		WhyScorePoorly:       he.buildWhyScorePoorly(hotspot),
		SuggestedImprovement: "Extract focused, testable functions for each concern. " +
			"Separate data transformation from side effects (logging, I/O). " +
			"Reduce nesting by extracting conditional blocks. " +
			"Consolidate repeated patterns into helpers.\n",
		BehaviorToPreserve: he.buildBehaviorToPreserve(hotspot.Path),
	}

	return exp, nil
}

// extractBehaviorPatterns extracts critical behavior from source code
func (he *HotspotsExplainer) extractBehaviorPatterns(filePath string) string {
	file, err := os.Open(filePath)
	if err != nil {
		return "- Unable to extract behavior patterns (file not accessible)\n"
	}
	defer file.Close()

	var patterns strings.Builder
	patterns.WriteString("- Error handling: Preserve error types and wrapping\n")
	patterns.WriteString("- Return values: Keep status codes and special cases\n")
	patterns.WriteString("- Cancellation: Preserve context cancellation handling\n")
	patterns.WriteString("- Dependencies: Keep critical imports and their usage\n")

	// Scan for specific patterns (regex)
	scanner := bufio.NewScanner(file)
	patterns_found := make(map[string]bool)

	statusCodeRx := regexp.MustCompile(`(?i)(http\.)?status(code)?.*\d{3}`)
	contextErrRx := regexp.MustCompile(`ctx\.Err\(\)`)
	errorTypeRx := regexp.MustCompile(`errors?\.Is\(|type.*Error`)

	for scanner.Scan() {
		line := scanner.Text()
		if statusCodeRx.MatchString(line) && !patterns_found["status"] {
			patterns.WriteString("- HTTP status codes: Different 4xx/5xx mappings per error type\n")
			patterns_found["status"] = true
		}
		if contextErrRx.MatchString(line) && !patterns_found["context"] {
			patterns.WriteString("- Context handling: Cancellation signals should be respected\n")
			patterns_found["context"] = true
		}
		if errorTypeRx.MatchString(line) && !patterns_found["errors"] {
			patterns.WriteString("- Error classification: Type assertions and special cases\n")
			patterns_found["errors"] = true
		}
	}

	return patterns.String()
}

// writeSummaryTable writes the hotspots summary table
func (he *HotspotsExplainer) writeSummaryTable(sb *strings.Builder, hotspots []FileReport) {
	if len(hotspots) == 0 {
		return
	}

	sb.WriteString("| Hotspot | Overall | Complexity | Structure | Duplication | Coupling |\n")
	sb.WriteString("|---------|---------|------------|-----------|-------------|----------|\n")

	for _, h := range hotspots {
		sb.WriteString(fmt.Sprintf("| %s | **%.1f** | %.1f | %.1f | %.1f | %.1f |\n",
			h.Path,
			h.Score,
			getFloatValue(h.Dimensions.Complexity),
			getFloatValue(h.Dimensions.Structure),
			getFloatValue(h.Dimensions.Duplication),
			getFloatValue(h.Dimensions.Coupling),
		))
	}
	sb.WriteString("\n")
}

func (he *HotspotsExplainer) scopedHotspots() []FileReport {
	if he.scope != "package" {
		return he.report.Hotspots
	}

	type aggregate struct {
		report FileReport
		count  int
	}
	groups := make(map[string]*aggregate)
	for _, file := range he.report.Files {
		if file.Score >= 8 {
			continue
		}
		group := groups[file.Package]
		if group == nil {
			group = &aggregate{report: FileReport{Path: file.Package, Package: file.Package}}
			groups[file.Package] = group
		}
		group.count++
		group.report.Score += file.Score
		group.report.Lines += file.Lines
		group.report.InternalImports += file.InternalImports
		group.report.DuplicateTokens += file.DuplicateTokens
		group.report.TotalFunctionTokens += file.TotalFunctionTokens
		group.report.Functions = append(group.report.Functions, file.Functions...)
		group.report.Health.IgnoredReturnValues += file.Health.IgnoredReturnValues
		group.report.Health.Panics += file.Health.Panics
		group.report.Health.TODOs += file.Health.TODOs
		group.report.Health.FIXMEs += file.Health.FIXMEs
		group.report.Health.Goroutines += file.Health.Goroutines
		group.report.Health.ChannelOperations += file.Health.ChannelOperations
		group.report.Dimensions = averageDimensions(group.report.Dimensions, file.Dimensions, group.count)
	}

	result := make([]FileReport, 0, len(groups))
	for _, group := range groups {
		group.report.Score /= float64(group.count)
		group.report.Classification = classification(group.report.Score)
		if group.report.TotalFunctionTokens > 0 {
			group.report.DuplicationPercent = float64(group.report.DuplicateTokens) * 100 / float64(group.report.TotalFunctionTokens)
		}
		result = append(result, group.report)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Score == result[j].Score {
			return result[i].Path < result[j].Path
		}
		return result[i].Score < result[j].Score
	})
	return result
}

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
		Complexity:  average(current.Complexity, next.Complexity),
		Structure:   average(current.Structure, next.Structure),
		Duplication: average(current.Duplication, next.Duplication),
		Coupling:    average(current.Coupling, next.Coupling),
	}
}

// getFloatValue returns float value or 0 if nil
func getFloatValue(val *float64) float64 {
	if val == nil {
		return 0
	}
	return *val
}
