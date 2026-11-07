---
name: go-code-health
description: Measure and explain structural code health in Go repositories using a deterministic, read-only analyzer that returns a score from 0.0 to 10.0. Use when asked for a Go code health score, maintainability assessment, complexity, long functions, exact duplication, internal package coupling, unhealthy files, or refactoring priorities. Do not use for security audits, correctness claims, test coverage, or non-Go projects.
compatibility: Requires Go 1.27.3+ (or automatic toolchain download) and POSIX sh. No external Go modules.
---

# Go Code Health

Score the Go source code using the bundled analyzer. **Never estimate a score yourself or substitute an LLM judgment for the numeric output.** The scoring model is a transparent, versioned heuristic, not an ISO-certified or industry-standard health score.

## Workflow

1. Identify the target Go repository or package directory. For a multi-module repository, prefer analyzing each module separately. Do not alter target files.
2. Run `scripts/analyze.sh /absolute/path/to/repository --format json` (run the script relative to **this skill directory**). The executable accepts a repository, directory, or individual `.go` file.
3. Check `analysis_status`. Only present `code_health_score` as the definitive score when `analysis_status` is `complete`. `partial` means some files could not be parsed; the aggregate score is `null`.
4. Report `code_health_score` **out of 10**, its `classification`, and the `scoring_version`. Include the number of analyzed Go files and exclusions.
5. Explain the four `dimensions`: `complexity`, `structure`, `duplication`, and `coupling`. Do **not** rename the branching complexity heuristic to Sonar cognitive complexity.
6. Prioritize `hotspots` and their grounded `findings`, citing file paths and line numbers when available. Distinguish measured data from possible refactoring suggestions.
7. If the user requests a fix, explain the plan first and modify code only with the user's permission. Rerun exactly the same analysis scope/options afterwards to compare scores.
8. If `coupling` is `null`, explain that no `go.mod` was found, so coupling was omitted and the weights of the other dimensions were renormalized.

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
```

Override `CLAUDE_SKILLS_DIR` to install to a project's `.claude/skills/` instead. The installer copies source and reference files only, not `.build/` artifacts. To preserve the analyzer's exact exit codes, use `scripts/analyze.sh` or the compiled binary; Make itself wraps failed recipe statuses.

## Suggested answer format

> **Go code health: 7.4/10 (Moderate)** — scoring model `go-v0.1.0`; analyzed 112 Go source files, excluding generated files and tests.
>
> **Main problems:** `internal/orders/service.go` (5.1/10), due to nested branching and long functions; `internal/payments/adapter.go` (6.2/10), due to repeated code blocks. **Suggested priority:** simplify the most complex functions before splitting modules.
>
> **Limitations:** exact token duplication and syntax-based heuristics only. This score does not establish correctness, security, or test quality.

Never invent file paths, findings, counts, or scores. Use the actual JSON output.

## Interpretation and limitations

- The score estimates **static structural maintainability**, not overall software quality.
- Four measured dimensions: branching/nesting complexity (40%), function/file structure (25%), exact token duplication (20%), and intra-module package fan-out (15%).
- Missing `go.mod`: no coupling score; remaining weights are renormalized.
- Generated files, vendor directories, hidden directories, `testdata`, and `*_test.go` are excluded by default. Test files may be included explicitly.
- Unsupported in v0.1: cohesion, semantic duplication, cross-module dependencies, architecture layer violations, Git change hotspots, trend history, and measured testability.
- Read [references/scoring-model.md](references/scoring-model.md) when users ask how the score is computed or need to assess its limitations.
- Read [references/response-schema.md](references/response-schema.md) for the output fields and CI behavior.
