package main

const defaultMaxFunctionLines = 80

type scoringPolicy struct {
	gotoCheck        bool
	maxFunctionLines int
	recursionCheck   bool
}

func (p scoringPolicy) effectiveComplexity(fn FunctionReport) int {
	value := fn.BranchingComplexity
	if p.gotoCheck {
		value += fn.GotoCount
	}
	if p.recursionCheck && fn.RecursiveCalls > 0 {
		value++
	}
	return value
}

func recursionAndGotoFindings(fn FunctionReport, policy scoringPolicy) []Finding {
	var findings []Finding
	if policy.gotoCheck && fn.GotoCount > 0 {
		findings = append(findings, Finding{"goto-usage", "warning", fn.Line,
			itoa(fn.GotoCount) + " goto statements in " + fn.Name})
	}
	if !policy.recursionCheck {
		return findings
	}
	if fn.RecursiveCalls > 0 && !fn.RecursionJustified {
		findings = append(findings, Finding{"undocumented-recursion", "warning", fn.Line,
			"Recursion in " + fn.Name + ": consider an iterative implementation, or document why recursion is preferable with chnq:recursion"})
	}
	if fn.RecursionDirective && fn.RecursiveCalls == 0 {
		findings = append(findings, Finding{"stale-recursion-directive", "info", fn.Line,
			"No direct recursion detected in " + fn.Name + "; review this directive (indirect calls and closures are not assessed)"})
	}
	return findings
}
