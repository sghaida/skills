# Advanced commands and execution

From the repository root, use the bundled wrapper:

```sh
# Recommended agent/CI gate: native policy evaluation and bounded details
./chnq/scripts/analyze.sh /path/to/project --check --format compact

# All dimensions, comparisons, and findings even when policy passes
./chnq/scripts/analyze.sh /path/to/project --check --format compact --verbose

# Full project context, focused display; exact package excludes descendants
./chnq/scripts/analyze.sh /path/to/project --check --format compact --file internal/orders/service.go
./chnq/scripts/analyze.sh /path/to/project --check --format compact --package internal/orders

# More findings (default 10); zero never hides failures or warnings
./chnq/scripts/analyze.sh /path/to/project --check --format compact --verbose --max-findings 20

# Full JSON, legacy terminal output, or standalone overall gate
./chnq/scripts/analyze.sh /path/to/project --format json
./chnq/scripts/analyze.sh /path/to/project --format text
./chnq/scripts/analyze.sh /path/to/project --fail-under 7

# Additional measurements and deterministic explanations
./chnq/scripts/analyze.sh /path/to/project --include-tests --include-git-churn
./chnq/scripts/analyze.sh /path/to/project --explain-hotspots --hotspots-scope package

# Bounded parsing and cache diagnostics
./chnq/scripts/analyze.sh /path/to/project --workers 20
./chnq/scripts/analyze.sh /path/to/project --cache-debug
./chnq/scripts/analyze.sh /path/to/project --cache-info
./chnq/scripts/analyze.sh /path/to/project --no-cache
```

In an installed skill, resolve `scripts/analyze.sh` from the skill directory.
The wrapper prefers the packaged native binary; source installations build
only the analyzer into a temporary directory, never target code. They require
Go 1.27.3 or automatic toolchain download with network access.

`--check` requires a valid version-1 policy with `minimum_score`. It reads
`include_tests`, enables churn for a configured churn gate, and rejects an
explicit conflicting `--include-tests` value. Other scans retain legacy
flag-based test/churn inclusion and optional `--fail-under` behavior.
An additional `--fail-under` gate cannot weaken a project-policy gate.

Passing unfiltered compact checks show only the summary, check count, and any
warnings. Failures and unavailable checks are always shown. `--verbose`
restores all comparisons, dimensions, and bounded findings. Failing, unchecked,
incomplete, and selected views retain findings without requiring `--verbose`.
Measured findings are prioritized by failed-gate relevance, then severity and
deterministic tie-breaks; synthetic hints only fill remaining budget.
The option changes compact presentation only, not JSON/text or scan/cache inputs.

For `--check`, `--file`, and `--package`, `--path` must be a complete project
directory, not a file or a directory inside an enclosing Go module. Use
module roots separately in multi-module repositories. Selectors are filesystem
paths relative to that root, not Go import paths; absolute paths inside it
are also accepted. Missing/excluded selections, outside paths, and combining
both selectors are errors. Filters work with JSON, text, and compact output.

## Cache and workers

The worker pool defaults to 10; `--workers N` accepts 1–256 without changing
report ordering or measurements. Cache files default to `.chnq-health-status`
inside the analyzed project root; keep them git-ignored. `--cache-file` changes
the location.

Exact report reuse requires the semantic scan fingerprint and the complete
eligible-file manifest (paths and content hashes) to match. The fingerprint
includes scan options, project policy, module/engine identity, and Git refs
when churn is enabled. Changed scans restore unchanged file-local snapshots
and recompute all project-wide metrics, keeping cached, incremental, and
uncached results semantically identical.

Presentation filters and findings limits do not alter scan inputs or cache
identity. Policy results and filtered views are applied after analysis, not
stored in place of complete cached reports. A scan still validates inputs;
filtering output does not promise constant-time execution.

`--cache-debug` writes HIT, INCREMENTAL, MISS, INVALIDATED, REBUILT, or BYPASSED
diagnostics to stderr. `--cache-info` inspects metadata without scanning and
cannot be combined with checks or selectors.

## Development

From the repository root use `make build`, `make test`, `make vet`, and
`make run TARGET=/path/to/project FORMAT=compact ARGS="--check"`.
The Go module is in `chnq/`. Install using the root installation commands
documented in [README.md](../../README.md).

Use the wrapper or binary for exact exit codes: Make wraps failed recipes in
its own nonzero status. External `jscpd-rs` selection requires `jscpd`; report
installation errors explicitly and never silently switch engines.
