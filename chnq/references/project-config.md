# Project Health Policy

Projects can store their code-health policy in a checked-in
`.go-code-health.json` file at the root of the repository being analyzed.

## Schema

```json
{
  "version": 1,
  "duplication_engine": "internal",
  "minimum_score": 7.0,
  "minimum_dimensions": {
    "complexity": 6.0,
    "structure": 7.0,
    "duplication": 8.0,
    "coupling": 6.0,
    "cyclic_dependencies": 8.0,
    "interface_compliance": 7.0,
    "duck_typing": 7.0
  },
  "health_thresholds": {
    "minimum_documentation_percent": 80.0,
    "maximum_ignored_return_values": 0,
    "maximum_panics": 0,
    "maximum_todos": 20,
    "maximum_fixmes": 0,
    "maximum_import_cycles": 0,
    "maximum_dependency_depth": 8,
    "maximum_incoming_dependencies": 20,
    "maximum_git_churn": 2000
  },
  "include_tests": false,
  "broad_interface_threshold": 5,
  "goto_check": true,
  "max_function_lines": 60,
  "recursion_check": true
}
```

- `version` is currently `1`.
- `duplication_engine` selects how duplicate code is measured:
  - `internal` uses the dependency-free Go token detector and is the default.
  - `jscpd-rs` invokes the installed `jscpd` executable for broader clone
    detection. Install it with `make install-jscpd-rs`.
- `minimum_score` is required and must be between `0.0` and `10.0`. It is the
  minimum acceptable aggregate health score for the complete project.
- `minimum_dimensions` is optional. Each configured dimension must be between
  `0.0` and `10.0`. A configured dimension is an additional gate; it cannot be
  offset by a higher score in another dimension.
- `health_thresholds` is optional. These thresholds apply to repository-level
  values in the report's `summary.health` object. `minimum_` thresholds pass
  when the measured value is greater than or equal to the threshold;
  `maximum_` thresholds pass when the measured value is less than or equal to
  the threshold.
- `include_tests` defaults to `false` when omitted.
- `broad_interface_threshold` controls how many methods make an interface
  broad. It defaults to `5` when omitted; the legacy parser also falls back
  to `5` for non-positive integers.
- `goto_check` defaults to `false`. When enabled, each goto contributes +1
  effective branching complexity and a per-function `goto-usage` warning.
  Labeled breaks/continues and fallthrough do not count.
- `max_function_lines` defaults to `80`; an explicitly configured value must
  be a positive integer. The long-function finding fires above this limit;
  the structure length penalty also begins above that same limit.
- `recursion_check` defaults to `false`. When enabled, each function with
  detected direct recursion contributes +1 complexity total and needs a
  non-empty `chnq:recursion` justification in its doc comment.
- The three new keys reject explicit nulls and invalid types. Missing keys
  use the documented defaults. In `go-v0.4.0`, length penalties start above
  the configured limit rather than half of it; scores may improve relative
  to the previous model.

The binary directly reads the duplication engine, broad-interface threshold,
and the three new scoring settings. Other fields are agent/CI policy:
pass `--include-tests` for `include_tests: true`, pass `--fail-under` for the
overall minimum, and compare dimension/health gates separately. JSON minimums
do not automatically change the binary's exit status.

See the [complete configuration example and calculations](../../README.md#project-policy-and-required-health-gate)
and [scoring model](scoring-model.md) for formulas.

## Recursion policy

```go
// chnq:recursion Recursive traversal is clearer than an explicit stack.
// A runtime depth guard rejects excessive nesting with an error.
func walk(n *Node, depth int) error { /* application implementation */ }
```

Both tail and non-tail detected direct recursion require documentation when
enabled. Missing/empty reasons produce `undocumented-recursion` (warning),
suggesting an iterative implementation or justification. A reason suppresses
only that warning, not the +1 complexity contribution. A directive with no
detected recursion produces `stale-recursion-directive` (info), qualified by
the checker's detection limits.

Reasons should explain why recursion is preferable and how excessive depth
is handled. Only presence is checked, never truthfulness or runtime safety.
Detection uses syntax-based lexical scopes for functions (including
generic functions) and current-receiver calls, without type-checking or
loading dependencies; explicitly reassigned receivers are conservatively
excluded. It does not cover nested closures,
aliases, other receivers, mutual recursion, or interface dispatch. Direct
goroutine launches are not stack recursion; self-calls in their evaluated
arguments still count. Go provides no guaranteed tail-call optimization.

Termination and depth bounds are not assessed. Runtime guards, when needed,
must return errors and be tested in application code. There is no
`max_recursion_depth` setting, SSA integration, or automatic rewrite.

Policy changes invalidate cached reports. Raw goto/recursion facts are
always measured and kept in reusable snapshots; policy-dependent scoring is
recomputed, so toggling checks cannot reuse stale scores.

The policy applies to the complete scan of the configured project scope.
`minimum_score` is compared with `code_health_score`; each configured
dimension is compared with its corresponding value in `dimensions`.

## Threshold descriptions

`minimum_score` and `minimum_dimensions` use the analyzer's 0.0–10.0 scale:
higher is healthier. Their thresholds are inclusive, so a measured value equal
to the threshold passes. `health_thresholds` use the metric-specific units and
directions described below.

### Overall score

- **`minimum_score`** is the project's overall health target. It combines the
  four weighted dimensions into one score from 0 to 10. A low score means the code
  needs structural improvement in one or more areas.
- **Example:** A minimum of `8.0` means the project must have an overall score
  of at least 8.0, even if some individual metrics are stronger than others.

### Complexity

- **`minimum_dimensions.complexity`** asks, "How hard is the code's logic to
  follow?" It measures branching decisions and nested control flow. A low
  score usually means functions have too many paths or too much indentation.
- **Simple example:** A function with several nested `if` statements, loops,
  and `switch` cases is harder to follow than a function with one clear
  decision. A complexity threshold of `8.0` requires the measured complexity
  score to remain at least 8.0.

### Structure

- **`minimum_dimensions.structure`** asks, "Is the code split into manageable
  pieces?" It considers file size, function length, and nesting. A low score
  usually means files or functions are too large or deeply nested.
- **Simple example:** One 150-line function is harder to maintain than three
  focused 40-line functions. A structure threshold of `8.0` requires files
  and functions to remain within the project's configured health level.

### Duplication

- **`minimum_dimensions.duplication`** asks, "Are we writing the same code in
  multiple places?" It detects repeated blocks of lexical tokens across
  different functions. A low score means changes may need to be repeated in
  several places.
- **Simple example:** If `CreateUser` and `CreateAdmin` contain the same
  validation and persistence block, fixing a bug may require changing both.
  A duplication threshold of `8.0` limits how much repeated code is allowed.

### Coupling

- **`minimum_dimensions.coupling`** asks, "How many other internal packages
  does each package depend on?" It measures internal package fan-out within a
  Go module. A low score can mean that packages are strongly connected, making
  changes harder to isolate.
- **Simple example:** A `checkout` package that imports `users`, `orders`,
  `payments`, `inventory`, and `shipping` is more dependent on the rest of the
  system than a package that imports only `orders` and `payments`. A coupling
  threshold of `8.0` limits excessive internal package dependencies.

Coupling is supported when the analyzed project contains a `go.mod`. If no
`go.mod` is found, the analyzer reports coupling as unavailable (`null`) and
the remaining metrics are reweighted. Do not configure a coupling threshold
for projects where coupling is not applicable.

### Cyclic dependencies

- **`minimum_dimensions.cyclic_dependencies`** uses a 0.0–10.0 score.
- A score of `10.0` means no analyzed package participates in an internal
  import cycle; cycle participation lowers the score.
- The existing `health.import_cycles` metric reports the number of files in
  participating packages.

### Interface compliance and duck typing

- **`minimum_dimensions.interface_compliance`** gates unused and broad
  interface declarations.
- **`minimum_dimensions.duck_typing`** gates evidence that concrete method sets
  satisfy declared interfaces.
- Both dimensions are independent 0.0–10.0 scores and do not change the
  existing overall score.
- `health.interface_count`, `health.unused_interfaces`,
  `health.broad_interfaces`, and `health.implicit_implementations` provide
  the underlying measurements.

### `include_tests`

- **`include_tests`** controls whether files ending in `_test.go` are included
  in the scan. It defaults to `false`.
- **Example:** Keep it `false` when the policy measures production code only.
  Set it to `true` when test code is part of the project's maintainability
  target.

## Additional health metrics

These metrics are reported in `summary.health` and can be configured under
`health_thresholds`. The threshold names intentionally describe the direction
of a passing result.

### Documentation coverage

- **`minimum_documentation_percent`** measures the percentage of exported
  functions, types, constants, and variables with GoDoc comments.
- A value of `80.0` requires at least 80% of exported symbols to be
  documented.
- A project with no exported symbols reports `0.0`; omit this threshold for
  internal packages that intentionally expose no public API.

### Error-handling findings

- **`maximum_ignored_return_values`** limits assignments that discard a call's
  return value with `_`.
- **`maximum_panics`** limits detected `panic` calls.
- These are syntax-based findings, not proof that every discarded value or
  panic is incorrect. Configure them when the project has a strict error
  handling policy.

### Work-item markers

- **`maximum_todos`** limits `TODO` comments.
- **`maximum_fixmes`** limits `FIXME` comments.
- These thresholds are useful for preventing unresolved work from accumulating.

### Package dependency health

- **`maximum_import_cycles`** limits analyzed files whose packages participate
  in internal import cycles. `0` is recommended.
- **`maximum_dependency_depth`** limits the longest internal dependency chain
  observed for a package.
- **`maximum_incoming_dependencies`** limits package fan-in. High fan-in can
  indicate a central package whose changes affect much of the repository, but
  it is not automatically a defect.

Currently `summary.health.dependency_depth` and `incoming_dependencies` are
not aggregated and remain zero. For meaningful gates on these two metrics,
compare file/package measurements explicitly (for example their maximum).
Do not treat zero summary fields as proof of no dependencies.

### Git churn

- **`maximum_git_churn`** limits changed lines reported by the optional
  `--include-git-churn` scan.
- This threshold is only meaningful when Git history is available and the
  option is enabled. Otherwise, omit it or treat the result as unavailable.
- Git churn is a risk indicator, not a quality score; a frequently changed
  file may be an important, healthy component.

### Descriptive-only metrics

`exported_symbols`, `goroutines`, and `channel_operations` are reported for
context but do not have generic pass/fail thresholds. Their desired values
depend on the package's role. Review them alongside hotspots and findings
instead of treating larger or smaller values as inherently healthier.

Use `minimum_score` for the required overall health level. Add dimension
thresholds when a project needs to enforce specific engineering priorities,
such as preventing complexity from being hidden by strong duplication or
structure scores.

An analysis is **passing** only when:

1. `analysis_status` is `complete`.
2. `code_health_score` is present and meets `minimum_score`.
3. Every configured dimension is present and meets its threshold.
4. Every configured `health_thresholds` value is present and meets its
   minimum or maximum comparison.

When a policy exists, the skill must run this check before every agentic task
or code change. If the check fails, code-health remediation takes priority:
the agent must update the code to meet at least the configured thresholds,
re-run the complete scan, and only then continue with unrelated work. If safe
remediation cannot meet the thresholds, the task is blocked and the agent must
report the remaining findings. Thresholds must not be lowered silently to make
a failing scan pass.

## Status bars

The skill should show the overall score and every configured threshold as a
horizontal bar, followed by the measured value and threshold. When the chat
renderer supports inline HTML/CSS, prefer screenshot-style bars with a dark
track and colored fill:

```html
<div>
  <strong>Code Health</strong>
  <strong>7.9 / 10</strong>
  <span>Healthy</span>
</div>
<div>
  <div>Complexity <span style="color:#d9a441">███████░░░</span> 6.1</div>
  <div>Structure <span style="color:#2a9d8f">████████░░</span> 8.0</div>
  <div>Duplication <span style="color:#c96b5c">██████░░░░</span> 5.8</div>
  <div>Coupling <span style="color:#2a9d8f">██████████</span> 10.0</div>
</div>
```

Use these colors consistently:

- Complexity: amber (`#d9a441`)
- Structure and coupling: teal (`#2a9d8f`)
- Duplication: muted red (`#c96b5c`)
- Unavailable metrics: gray

If HTML/CSS is sanitized, use this fallback:

```text
Complexity  [██████░░░░] 6.1/10 < 7.0 🟥
Structure   [████████░░] 8.0/10 ≥ 7.0 🟩
```

Keep the numeric score and threshold next to every bar. Bars are visual aids
and must not replace the measured values or pass/fail status.

If a configured dimension is unavailable, such as `coupling` when the target
has no `go.mod`, the policy is not passing. Remove that dimension from the
policy if it is not applicable to the project.

## First-run setup

When `.go-code-health.json` does not exist:

1. Run a complete JSON scan before making code changes.
2. Show the user the measured overall score and dimensions.
3. Ask the user which duplication engine to use: `internal` (recommended
   default) or `jscpd-rs` (requires installation).
4. Ask the user to choose `minimum_score` and any applicable dimension
   thresholds. Do not silently choose or persist engine choices or thresholds.
5. Create `.go-code-health.json` at the project root with the agreed values.
6. Ask the user to commit the policy with the project so it applies to future
   coding sessions and contributors.

The initial scan is informational; it establishes the baseline and does not
pass or fail the project until the user confirms the policy.

### First-run example

Suppose the first scan reports:

```text
Overall:     9.5
Complexity:  9.2
Structure:   9.4
Duplication: 9.8
Coupling:    10.0
```

The user might choose `8.0` as the minimum for every metric. The skill then
creates this file in the project root:

```json
{
  "version": 1,
  "duplication_engine": "internal",
  "minimum_score": 8.0,
  "minimum_dimensions": {
    "complexity": 8.0,
    "structure": 8.0,
    "duplication": 8.0,
    "coupling": 8.0
  },
  "include_tests": false
}
```

If a later scan reports an overall score of `8.4` but a complexity score of
`7.6`, the project does not pass. The agent must improve the code until
complexity reaches at least `8.0`, or report that the configured policy cannot
currently be met.
