---
name: go-code-health
description: Measure and explain structural code health in Go repositories using a deterministic, read-only analyzer that returns a score from 0.0 to 10.0. Use when asked for a Go code health score, maintainability assessment, complexity, long functions, exact duplication, internal package coupling, unhealthy files, or refactoring priorities. Do not use for security audits, correctness claims, test coverage, or non-Go projects.
compatibility: Requires Go 1.27.3+ (or automatic toolchain download) and POSIX sh. No external Go modules.
---

# Go Code Health

Score the Go source code using the bundled analyzer. **Never estimate a score yourself or substitute an LLM judgment for the numeric output.** The scoring model is a transparent, versioned heuristic, not an ISO-certified or industry-standard health score.

## Workflow

1. Identify the checked-out target repository or package directory. For a
   multi-module repository, prefer analyzing each module separately. Do not
   alter target files except for the project policy file described below.
2. Look for `.go-code-health.json` at the target project root. This is a
   project-owned, checked-in policy file; read
   [references/project-config.md](references/project-config.md) for its schema.
3. If the policy file does not exist, run a complete JSON scan before making
   code changes. Show the measured score and dimensions, ask the user which
   duplication engine to use (`internal`, the dependency-free default, or
   `jscpd-rs`, which requires installation), and ask for the acceptable
   overall and applicable dimension thresholds. Then create the policy file
   with the agreed values. Never invent or silently persist engine choices or
   thresholds. For example, if the baseline is 9.5 overall and all dimensions
   are above 9.0, the user might choose `internal` and 8.0 for the overall and
   each applicable dimension; the resulting policy is shown in
   [references/project-config.md](references/project-config.md).
4. When `.go-code-health.json` exists, every agentic task or code change must
   begin by running the complete JSON scan over the configured project scope.
   Always ask this skill for the current status rather than estimating health
   from the diff or from memory.
5. Treat the project as not passing when `analysis_status` is not `complete`,
   `code_health_score` is missing, the overall score is below
   `minimum_score`, or any configured dimension is below its threshold.
6. If the pre-task scan fails the policy, prioritize code-health remediation:
   update the code to meet at least the configured overall and dimension
   thresholds before proceeding with unrelated requested changes. Re-run the
   complete scan after each remediation cycle. If the thresholds cannot be
   reached safely, stop and report the blocking findings instead of claiming
   the task is complete.
7. Report `code_health_score` **out of 10**, its `classification`, the
   `scoring_version`, configured thresholds, and whether the policy passes.
   Include the number of analyzed Go files and exclusions.
8. If the user requests a code change, explain the plan first and modify code
   only with the user's permission. Run the same complete scan after changes
   and do not declare the task complete while the policy is failing. If a
   change intentionally lowers a score, ask the user to approve updated
   thresholds and update the project policy explicitly.
9. Explain the four `dimensions`: `complexity`, `structure`, `duplication`,
   and `coupling`. Do not rename the branching complexity heuristic to Sonar
   cognitive complexity.
10. Prioritize `hotspots` and their grounded `findings`, citing file paths and
   line numbers when available. Distinguish measured data from possible
   refactoring suggestions.
11. If `coupling` is `null`, explain that no `go.mod` was found. A configured
    coupling threshold is not passing when coupling is unavailable; otherwise,
    explain that the dimension was omitted and remaining weights were
    renormalized.
12. Show the overall score and every configured threshold as a horizontal
    status bar. Prefer screenshot-style colored bars using inline HTML/CSS
    when the chat renderer supports it: amber for complexity, teal for
    structure and coupling, muted red for duplication, and gray for
    unavailable metrics. If HTML/CSS is sanitized, use the documented
    10-segment Unicode fallback. Always include the numeric measured value,
    threshold, and pass/fail status beside each bar.
13. Review hotspots and low-structure packages for possible god objects. In
    Go, inspect structs and their methods for one type that owns many unrelated
    responsibilities, has a broad state surface, or coordinates unrelated
    concerns such as I/O, parsing, business rules, and reporting. Report the
    type, file, line, responsibilities, and evidence for the concern.
14. Treat god-object findings as qualitative design-review observations, not
    analyzer scores. Do not claim that a type is a god object based only on
    line count, method count, or a hotspot score; explain the observed
    responsibilities and suggest focused extractions when appropriate.
15. Review hotspot files and packages for mixed responsibilities. Look for
    unrelated concerns in one file or package, such as file/network I/O,
    parsing, business rules, persistence, formatting, test assertions, and
    reporting. Report the file or package, each observed responsibility, the
    evidence supporting the observation, and a possible focused split.
16. Use the duplication engine recorded in `.go-code-health.json`. The
    `internal` engine is dependency-free. The `jscpd-rs` engine invokes the
    external `jscpd` command; if it is selected but unavailable, report the
    setup error and do not silently fall back to the internal engine.

## Run

```sh
# JSON for agents and automation (default format)
./scripts/analyze.sh /path/to/go/repository --format json

# Readable terminal summary
./scripts/analyze.sh /path/to/go/repository --format text

# Optional test code inclusion
./scripts/analyze.sh /path/to/go/repository --include-tests

# CI gate: exit 3 if score < 7.0
./scripts/analyze.sh /path/to/go/repository --fail-under 7
```

For a project policy, read `.go-code-health.json`, use its
`duplication_engine`, and pass its `include_tests` setting to the scan. Use
`--fail-under` for the configured `minimum_score`, then compare every
configured `minimum_dimensions` threshold with the JSON report.

## Metric explanations for engineers

Use these simple explanations when asking an engineer to choose thresholds:

- **Overall score:** The combined health of the project. A minimum of `8.0`
  means the overall score must remain at least 8.0.
- **Complexity:** How hard the logic is to follow. A function with many nested
  `if` statements, loops, and `switch` cases is more complex than one with a
  single clear decision.
- **Structure:** Whether files and functions are manageable. One 150-line
  function is less manageable than three focused 40-line functions.
- **Duplication:** Whether the same code is repeated. If `CreateUser` and
  `CreateAdmin` contain the same validation block, a bug fix may be needed in
  both places.
- **Coupling:** How many other internal packages a package depends on. A
  `checkout` package importing `users`, `orders`, `payments`, `inventory`, and
  `shipping` is more coupled than one importing only `orders` and `payments`.
- **`include_tests`:** Whether `_test.go` files are included. Keep it `false`
  for production-code health only, or set it to `true` when test code is part
  of the maintainability target.

Requirements: Go 1.27.3 or newer and a POSIX shell. An older Go installation can auto-download the required toolchain when network access is allowed. The command reads source files but **never builds, runs, or tests the analyzed repository**. The wrapper builds only this skill's bundled analyzer using Go's standard library. It may download the Go toolchain if the installed one is older. It returns exit code 0 for complete analysis, 2 for input/parse/incomplete analysis errors, and 3 for a failing `--fail-under` threshold.

## Makefile workflows

The **only** Go module manifest is `chnq/go.mod` (Go 1.27.3). From this skill directory:

```sh
make deps             # Go dependency download (none currently)
make build            # Builds .build/go-code-health
make test             # Run tests
make vet              # Run go vet
make run TARGET=/path/to/repo FORMAT=json
make cmd TARGET=/path/to/repo FORMAT=text
make install-claude   # Install to ~/.claude/skills/go-code-health/
make install-jscpd-rs # Install the optional jscpd-rs duplication engine
```

The project policy file is created in the target repository, not in this skill
directory. Commit `.go-code-health.json` with the target project after agreeing
on its thresholds.

Override `CLAUDE_SKILLS_DIR` to install to a project's `.claude/skills/` instead. The installer copies source and reference files only, not `.build/` artifacts. To preserve the analyzer's exact exit codes, use `scripts/analyze.sh` or the compiled binary; Make itself wraps failed recipe statuses.

## Suggested answer format

> **Go code health: 7.4/10 (Moderate)** — scoring model `go-v0.1.0`; analyzed 112 Go source files, excluding generated files and tests.
>
> **Main problems:** `internal/orders/service.go` (5.1/10), due to nested branching and long functions; `internal/payments/adapter.go` (6.2/10), due to repeated code blocks. **Suggested priority:** simplify the most complex functions before splitting modules.
>
> **Limitations:** exact token duplication and syntax-based heuristics only. This score does not establish correctness, security, or test quality.

Never invent file paths, findings, counts, or scores. Use the actual JSON output.

### Threshold status example

```html
<div>
  <div>Complexity <span style="color:#d9a441">███████░░░</span> 6.1</div>
  <div>Structure <span style="color:#2a9d8f">████████░░</span> 8.0</div>
  <div>Duplication <span style="color:#c96b5c">██████░░░░</span> 5.8</div>
</div>
```

## Interpretation and limitations

- The score estimates **static structural maintainability**, not overall software quality.
- Four measured dimensions: branching/nesting complexity (40%), function/file structure (25%), exact token duplication (20%), and intra-module package fan-out (15%).
- Missing `go.mod`: no coupling score; remaining weights are renormalized.
- Generated files, vendor directories, hidden directories, `testdata`, and `*_test.go` are excluded by default. Test files may be included explicitly.
- Unsupported in v0.1: cohesion, semantic duplication, cross-module dependencies, architecture layer violations, Git change hotspots, trend history, and measured testability.
- Read [references/scoring-model.md](references/scoring-model.md) when users ask how the score is computed or need to assess its limitations.
- Read [references/response-schema.md](references/response-schema.md) for the output fields and CI behavior.
