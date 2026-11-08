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
    "coupling": 6.0
  },
  "include_tests": false
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
- `include_tests` defaults to `false` when omitted.

The policy applies to the complete scan of the configured project scope.
`minimum_score` is compared with `code_health_score`; each configured
dimension is compared with its corresponding value in `dimensions`.

## Threshold descriptions

All thresholds use the analyzer's 0.0–10.0 scale: higher is healthier. The
threshold is inclusive, so a measured value equal to the threshold passes.

### Overall score

- **`minimum_score`** is the project's overall health target. It combines all
  available metrics into one score from 0 to 10. A low score means the code
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

### `include_tests`

- **`include_tests`** controls whether files ending in `_test.go` are included
  in the scan. It defaults to `false`.
- **Example:** Keep it `false` when the policy measures production code only.
  Set it to `true` when test code is part of the project's maintainability
  target.

Use `minimum_score` for the required overall health level. Add dimension
thresholds when a project needs to enforce specific engineering priorities,
such as preventing complexity from being hidden by strong duplication or
structure scores.

An analysis is **passing** only when:

1. `analysis_status` is `complete`.
2. `code_health_score` is present and meets `minimum_score`.
3. Every configured dimension is present and meets its threshold.

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
