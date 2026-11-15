---
name: go-code-health
description: Measure structural code health in Go projects, packages, or files using a deterministic analyzer. Use for maintainability scores, complexity, long functions, exact duplication, coupling, and refactoring priorities. Not for security, correctness, test coverage, or non-Go projects.
compatibility: Release packages require POSIX sh; source checkouts also require Go 1.27.3+ or automatic toolchain download. Standard library only.
---

# Go Code Health

Use measured analyzer results; never invent scores, paths, or findings. The
versioned heuristic measures structural maintainability, not overall quality.
The analyzer must not build, execute, test, or modify target source code.
During authorized implementation or remediation, run the project's normal
tests and validation separately.

## Required workflow

1. Identify the complete project root; analyze Go modules separately.
2. If `.go-code-health.json` is missing, run a complete baseline with
   `scripts/analyze.sh <root> --format compact`. Explain the dimensions, ask
   for the duplication engine and overall/applicable dimension thresholds,
   then create the policy with agreed values. Read
   [project-config.md](references/project-config.md); never invent settings.
3. For health requests and before modifying Go source, run:
   `scripts/analyze.sh <root> --check --format compact`.
   The binary reads policy JSON, applies test inclusion, enables required Git
   churn, and checks all configured thresholds. Do not duplicate its comparisons
   in LLM reasoning or ad-hoc scripts.
4. Exit 0 means the check passed; 2 means invalid/incomplete analysis; 3 means
   a gate failed. Missing required measurements never pass. Without `--check`,
   exit 0 does not establish policy compliance.
5. Report baseline failures; obtain approval before fixing unrelated existing
   violations. They do not automatically block the requested work. Use baseline
   evidence to distinguish existing failures from new regressions; exit codes
   alone cannot establish that distinction. Address introduced regressions
   within scope or report them as unresolved blockers.
6. After any Go source changes, rerun the same complete-project policy check.
   Batch related fixes before rescanning. Report implementation/test results
   separately from policy compliance: work may be complete with disclosed
   pre-existing failures, but never claim the policy passes when it does not.
   Never silently change thresholds.
7. Report the measured score (0-10), scoring version, policy status, failed or
   unavailable checks, relevant findings, and analysis warnings. A concise
   passing summary is sufficient. Distinguish suggestions from measurements.

## On-demand views and efficiency

- Add `--file <relative.go>` or `--package <relative-directory>` when asked
  about that scope. Keep `<root>` unchanged: analysis/cache and policy remain
  project-wide; only displayed details are filtered. Packages exclude child
  packages. A selected score is not a project-policy verdict.
- Passing compact checks show a short summary; `--verbose` adds all dimensions,
  comparisons, and findings. Failures, unavailable checks, and analysis warnings
  remain visible. Failing, unchecked, or scoped views default to 10 findings;
  use `--max-findings N` to adjust. Measured gate-relevant findings precede
  fallback investigation hints.
- Reuse the default git-ignored `.chnq-health-status` cache. Read only relevant
  source regions and references; use full JSON or explanations when needed.
  Let the analyzer verify cache validity; do not assume unchanged inputs or
  substitute a remembered result for required before/after checks.
- Deep architectural/god-object reviews and elaborate formatting are optional:
  use them when requested or needed to investigate relevant findings.
- Read [reporting.md](references/reporting.md) for detailed reporting and
  [remediation.md](references/remediation.md) for remediation or qualitative
  review guidance only when needed.
- Read [advanced-workflows.md](references/advanced-workflows.md) for commands,
  caching, workers, and source/release execution; [scoring-model.md](references/scoring-model.md)
  for formulas and recursion/goto limitations; and
  [response-schema.md](references/response-schema.md) for output contracts.
