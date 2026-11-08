# JSON Response Contract — v1.1

`./scripts/analyze.sh <repo> --format json` emits one JSON report. Key fields:

| Field | Type | Description |
|---|---|---|
| `schema_version` | string | Output contract version, currently `1.0` |
| `scoring_version` | string | Formula identifier, currently `go-v0.3.0` |
| `language` | string | Always `go` in this implementation |
| `max_score` | number | Always 10 |
| `repository` | string | Absolute filesystem path scanned |
| `code_health_score` | number or null | Repository score (rounded to 0.1), null if incomplete |
| `classification` | string | excellent/healthy/moderate/unhealthy/critical, or unavailable |
| `analysis_status` | string | `complete` or `partial` |
| `dimensions` | object | Four weighted dimensions plus cyclic dependencies, interface compliance, and duck typing; each 0–10 or null |
| `summary` | object | Go files, lines, functions, token duplication, exclusions, and health measurements |
| `packages` | array | Per-directory package score, line counts, internal dependency fan-out/fan-in, and dependency depth |
| `files` | array | Per-file score, function measurements, duplication, health measurements, and findings |
| `hotspots` | array | Up to 5 lowest-scoring files under 8, with their full details |
| `git_hotspots` | array | Present with `--include-git-churn`; up to 5 files with the most changed lines |
| `configuration` | object | Include-tests, Git churn, duplication window, engine, and broad-interface threshold settings |
| `warnings` | array | Analysis warnings, including missing `go.mod` or parse failures |

`files[].functions[]` includes names, starting line, source line count, `branching_complexity`, and `max_nesting`. `files[].findings[]` includes `rule`, `severity`, optional `line`, and `message`. `files[].dimensions` is the same seven-dimension breakdown as the repository. The three architecture dimensions are reported independently and do not change `code_health_score`.

The `health` object includes exported symbols, documentation coverage,
discarded return values, panics, TODO/FIXME markers, goroutines, channel
operations, import cycles, dependency depth, incoming dependencies, interface
counts, unused and broad interfaces, implicit implementations, and optional
Git changed-line counts. Health measurements do not affect the existing
four-dimensional score.

## Exit codes

- `0`: complete scan and score at or above optional threshold.
- `2`: invalid arguments, no eligible Go source, build failure, or incomplete/partial scan. Partial scans can return JSON with `code_health_score: null`.
- `3`: complete analysis, but `code_health_score` is below the supplied `--fail-under` threshold.

For CI, never treat exit code 2 as a code health regression; it is a scanner failure or an incomplete analysis and should be investigated independently.

## Stable representation

Paths within `files` and `packages` are relative to the scanned directory and sorted lexicographically. Function and finding records are stable in source order and rule name order. File-based aggregate scoring is deterministic for the same input files and scoring version. The absolute `repository` field varies by environment.
