# JSON Response Contract — v1.1

`./scripts/analyze.sh <repo> --format json` emits one JSON report. Key fields:

| Field | Type | Description |
|---|---|---|
| `schema_version` | string | Output contract version, currently `1.0` |
| `scoring_version` | string | Formula identifier, currently `go-v0.4.0` |
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
| `configuration` | object | Include-tests, Git churn, duplication window, engine, broad-interface threshold, `goto_check`, `max_function_lines`, and `recursion_check` settings |
| `warnings` | array | Analysis warnings, including missing `go.mod` or parse failures |
| `policy` | optional object | With `--check`: project-wide `status` (`PASS`, `FAIL`, `ERROR`) and every configured comparison in `checks` |
| `selection` | optional object | With `--file`/`--package`: selected `kind`, relative `path`, score, classification, dimensions, file count, and lines |

`files[].functions[]` includes names, starting line, source line count, `branching_complexity`, and `max_nesting`. `files[].findings[]` includes `rule`, `severity`, optional `line`, and `message`. `files[].dimensions` is the same seven-dimension breakdown as the repository. The three architecture dimensions are reported independently and do not change `code_health_score`.

Function records also include nonzero `goto_count`, `recursive_calls`, and
`effective_branching_complexity`, plus `recursion_directive` and
`recursion_justified` when true. Zero/false values for these additive fields
are omitted. Raw counts are policy-independent; effective complexity includes
enabled goto and recursion contributions. Raw `branching_complexity` keeps
its original meaning. Justification flags describe comment presence, not a
termination or depth-safety assessment.

The `health` object includes exported symbols, documentation coverage,
discarded return values, panics, TODO/FIXME markers, goroutines, channel
operations, import cycles, dependency depth, incoming dependencies, interface
counts, unused and broad interfaces, implicit implementations, and optional
Git changed-line counts. Health measurements do not affect the existing
four-dimensional score.

## Native policy and focused views

`--check` evaluates policy in the analyzer. Each `policy.checks` entry contains
`name`, `measured` (number or null), `operator` (`>=` or `<=`), `threshold`,
and `status` (`PASS`, `FAIL`, `UNAVAILABLE`). `minimum_score` comes first,
then dimensions and health thresholds sorted by name. Required unavailable
measurements fail the policy; invalid/incomplete analysis produces `ERROR`.
Dependency depth and fan-in checks use package maxima, not legacy zero
summary values.

`--file relative.go` selects one file; `--package relative-directory` selects
an exact package directory, excluding descendants. Analysis, the cache,
top-level score/dimensions/summary, and policy remain complete-project.
`selection` describes the focused score; `files`, `packages`, and `hotspots`
contain only the selected scope (a file view retains its enclosing package
record). Focused `hotspots` contains selected files regardless of score;
`git_hotspots` is omitted. Missing/excluded selections are errors, never empty
successful reports. A selected file/package does not receive a separate
project-threshold pass/fail verdict.

## Compact output

`--format compact` returns project analysis status, overall score, classification,
scoring version, file/exclusion counts, seven dimensions, optional selected
score/dimensions, project-policy status, every configured comparison, all
analysis warnings, and bounded findings. Without `--check` it explicitly says
`NOT CHECKED`; an optional `--fail-under` verdict is labeled separately.

`--max-findings N` defaults to 10; 0 hides detailed findings only. The count
line reports shown/available entries. Every policy comparison and warning
remains visible regardless of this limit. Candidates come from all analyzed
files, not only the legacy under-8 hotspots. They include measured findings
and file/dimension scores below project targets as investigation hints, not
claims that project policy applies separately to files. They are ordered by
file score, then path, then existing finding/check order.

The limit affects compact output only; JSON/text retain their detailed forms.
Existing unfiltered JSON/text scans without `--check` retain their contracts.

## Exit codes

- `0`: complete scan and score at or above optional threshold; with `--check`,
  all project-policy gates passed.
- `2`: invalid arguments, no eligible Go source, build failure, or incomplete/partial scan. Partial scans can return JSON with `code_health_score: null`.
- `3`: complete analysis, but a configured project gate (`--check`) or the
  supplied `--fail-under` threshold failed.

For CI, never treat exit code 2 as a code health regression; it is a scanner failure or an incomplete analysis and should be investigated independently.

## Stable representation

Paths within `files` and `packages` are relative to the scanned directory and sorted lexicographically. Function and finding records are stable in source order and rule name order. File-based aggregate scoring is deterministic for the same input files and scoring version. The absolute `repository` field varies by environment.
