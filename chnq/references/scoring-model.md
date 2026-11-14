# Go Code Health Scoring Model (go-v0.4.0)

## Scope and intent

This is a **custom, experimental maintainability heuristic**. It is not SonarQube's Maintainability Rating, CodeScene's Code Health metric, ISO/IEC 25010 certification, or any proprietary metric. It offers a repeatable 0–10 signal and evidence of refactoring opportunities for Go code. It does not measure correctness, security, test effectiveness, runtime performance, or developer productivity.

**Raw measurements remain in JSON**, alongside rounded dimension and file scores. All thresholds and weights may change in future versions, which will use a different `scoring_version`.

## Dimensions and weights

| Dimension | Weight | How measured |
|---|---:|---|
| `complexity` | 0.40 | AST branching and nesting heuristic per function, combined into a file score |
| `structure` | 0.25 | Function length, conditional nesting, and overall file length |
| `duplication` | 0.20 | Exact lexical token blocks (35-token window) repeated in different functions |
| `coupling` | 0.15 | Number of distinct imports from the same Go module, aggregated by package directory |

The architecture dimensions `cyclic_dependencies`, `interface_compliance`, and
`duck_typing` are also reported on the 0–10 scale. They are independently
thresholded and are not included in the existing overall score.

For each file, the 0–10 overall score is `sum(dimension_score × weight) / sum(available_weights)`, rounded to one decimal place. Each component is bounded to 0–10. If no `go.mod` can be found in the target path or any of its parent directories, `coupling` is `null` and the other weights are renormalized to 1.0.

### Complexity

The AST-based **branching complexity** is a custom heuristic, not canonical cognitive complexity or the McCabe cyclomatic number:

- Each `if`, `for`, `range`, `switch`, type switch, or `select`: `1 + current_nesting_depth`.
- Explicit `case` or `select` communication clauses: +1 per clause (default clauses excluded).
- Every boolean `&&` or `||`: +1.
- Nested closure bodies are independently measured rather than counted in their parent function.
- `goto_check: true` adds +1 per goto to effective complexity.
- `recursion_check: true` adds +1 per function with detected direct recursion,
  not per call site, even when documented. Tail recursion is not exempt.
  Both checks default to false. Raw counts are always measured.

`effective_complexity = branching_complexity + (goto_check ? goto_count : 0) + (recursion_check && recursive_calls > 0 ? 1 : 0)`

A function is assigned a component score from effective complexity as follows:

| Branching complexity | Function score |
|---:|---:|
| 0–5 | 10 |
| 6–10 | 8 |
| 11–15 | 6 |
| 16–25 | 4 |
| 26–40 | 2 |
| >40 | 0 |

The file complexity score is **80% length-square-root-weighted mean of its function scores + 20% worst function score**. Files without functions score 10 in this component because no function branching has been observed, not because the file has been proven healthy.

### Structure

For each function, calculate a penalty:

`min(10, 0.08 × max(0, function_lines − max_function_lines) + 1.25 × max(0, nesting_depth − 3))`

`max_function_lines` defaults to 80 and must be a positive integer. No length
penalty applies at or below that limit; each excess line adds 0.08 before
the combined function-penalty cap. Function lines are physical lines from the
declaration through the closing brace, including blank/comment lines but not
the preceding doc comment.

For the file, calculate:

`file_length_penalty = min(2.5, 0.005 × max(0, file_lines − 400))`

`structure = clamp(10 − 0.6 × mean(function_penalties) − 0.4 × worst(function_penalties) − file_length_penalty, 0, 10)`

For files without functions, the function penalty is zero. Exceeding
`max_function_lines` function lines, depth 4, or 600 file lines generates an
explanatory finding where applicable.

Version `go-v0.4.0` intentionally moves the length-penalty baseline from half
the configured limit to the exact limit. Nesting and file-length penalties
are unchanged. Earlier scores may be lower; old-model caches are invalidated
through the scoring version.

### Recursion findings

When enabled, detected direct recursion without a non-empty
`// chnq:recursion <reason>` doc-comment directive produces
`undocumented-recursion`. A reason suppresses the warning, not the complexity
contribution. A directive without detected recursion produces a qualified
`stale-recursion-directive` informational finding.

Only confidently resolved same-file direct calls and current-receiver method
calls are covered; shadowed names are not self-calls. Nested closures,
aliases, explicitly reassigned receivers, other receivers, mutual recursion,
and interface dispatch are outside coverage. Direct goroutine launches are
excluded, but their evaluated argument calls are inspected.
Neither documentation nor finding a base-case return proves termination or
stack safety. Go provides no guaranteed tail-call optimization. Depth-limit
verification, SSA integration, and automatic refactoring are deferred.

### Duplication

Scan function bodies using Go's lexical scanner, ignore comments and semicolons, and retain exact identifier and literal spellings. A 35-token contiguous sequence appearing in **different functions** counts as a clone. Overlapping windows are unioned so their token coverage is counted only once per function. Nested closure bodies are removed from their enclosing function's sequence to avoid artificial self-cloning.

`duplication_percent = 100 × (tokens covered by clone windows / total function tokens)`

`duplication_score = clamp(10 − duplication_percent / 10, 0, 10)`

This intentionally does **not** detect renamed-variable clones, semantic duplication, copy/paste shorter than 35 tokens, or repeated code within a single function. Some boilerplate may be flagged.

### Coupling

Find the enclosing `go.mod`. For each package directory, count the number of **distinct imports starting with `<module-path>/`** across all files in that directory. Standard-library and third-party imports are excluded.

`coupling_score = clamp(10 − 1.25 × max(0, distinct_internal_package_imports − 4), 0, 10)`

Each file receives its enclosing package's coupling score. This is a simple structural fan-out proxy: it does not measure symbol-level dependencies, interface abstractions, coupling intensity, architectural boundaries, or semantic cohesion. It does not separately resolve nested Go modules.

### Architecture dimensions

- `cyclic_dependencies` is `10` when a package does not participate in an
  internal import cycle and `0` when it does.
- `interface_compliance` starts at `10` and is reduced for unused interfaces
  and interfaces exceeding the configured broad-interface threshold.
- `duck_typing` is `10` when every declared non-empty interface has matching
  concrete method-set evidence in its package; unmatched interfaces reduce the
  score proportionally.
- `broad_interface_threshold` defaults to `5` methods and can be set in
  `.go-code-health.json`.

## Aggregation

- **File:** four weighted dimensions combined with the above weights, plus
  three independently reported architecture dimensions.
- **Package:** source-line-weighted mean of file scores in the package directory.
- **Repository:** source-line-weighted mean of file scores for all analyzed source files.
- At aggregation time, each file has weight `max(1, source_lines)`; score rounding to one decimal takes place at each output level.
- Repository dimension scores are independently source-line-weighted from their file dimensions, and may vary by 0.1 from the weighted sum due to intermediate rounding.
- The five lowest-scoring files **below 8/10** are surfaced in `hotspots`; these are maintainability hotspots only, not churn-based hotspots.

## Labels

| Range | Label |
|---|---|
| [9.0, 10] | excellent |
| [8.0, 9.0) | healthy |
| [6.0, 8.0) | moderate |
| [4.0, 6.0) | unhealthy |
| [0, 4.0) | critical |

The label cutoffs are product defaults, not an accepted market standard. Meaningful comparability between teams or repositories requires calibration on manually reviewed Go codebases and version-locked scoring. A score of 10 means **no issues detected by these heuristics**, not that the code is perfect.

## Analysis coverage

The analyzer uses `go/parser` and `go/ast` to parse source without compiling, resolving types, running code, or fetching dependencies. By default it excludes `*_test.go`, generated files with the standard header, symlinks, `vendor`, `testdata`, `third_party`, `node_modules`, and hidden directories. It does not evaluate build-tag conditions and can include mutually exclusive platform source files. A directory with no eligible Go files is an error. Parse/read errors make the result `partial`, with `code_health_score: null` and exit 2; this prevents an incomplete scan from claiming a healthy aggregate.
