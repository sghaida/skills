# Skills

This repository contains reusable agent skills that extend coding agents with
focused workflows, project guidance, and practical developer tooling. Each
skill is self-contained so it can be installed and used independently in an
agent-enabled development environment.

## `chnq` — Code Health and Quality

`chnq` is a portable Agent Skill with a dependency-free Go static analyzer. It
returns a deterministic, explainable 0–10 structural maintainability score at
repository, package, and file levels.

### Quick start

The analyzer requires Go 1.27.3. It has no third-party Go dependencies.

From the repository root, analyze a Go repository as text:

```sh
./chnq/scripts/analyze.sh /path/to/your/go/repo --format text
```

Produce a JSON report:

```sh
./chnq/scripts/analyze.sh /path/to/your/go/repo --format json > health.json
```

Use it as a CI quality gate. This command exits with code 3 when the score is
below the requested threshold:

```sh
./chnq/scripts/analyze.sh /path/to/your/go/repo --fail-under 7
```

Scans use a local `.chnq-health-status` cache (ignored by Git). Unchanged
repositories reuse the cached report; when the cache detects a partial change,
only changed files are rescanned and merged with unchanged results. File
parsing uses 10 workers by default and can be tuned with `--workers 1-256`.
Use `--no-cache` for a complete rescan:

```sh
./chnq/scripts/analyze.sh /path/to/your/go/repo --workers 20
./chnq/scripts/analyze.sh /path/to/your/go/repo --no-cache
```

Generate deterministic hotspot explanations without an LLM. The default scope
is files; `package` aggregates low-scoring files by enclosing package:

```sh
./chnq/scripts/analyze.sh /path/to/your/go/repo \
  --explain-hotspots --hotspots-scope package
```

### Project policy and required health gate

For ongoing agentic development, keep a checked-in `.go-code-health.json` at
the root of each project being analyzed. The policy records the acceptable
overall score and, optionally, minimum scores for individual dimensions:

```json
{
  "version": 1,
  "duplication_engine": "internal",
  "minimum_score": 7.0,
  "minimum_dimensions": {
    "complexity": 6.0,
    "structure": 7.0,
    "duplication": 8.0
  },
  "include_tests": false
}
```

On the first run, choose the duplication engine:

- `internal` (recommended default): dependency-free Go token detection
- `jscpd-rs`: invokes the installed `jscpd` executable for broader clone
  detection

Install the optional engine with:

```sh
make install-jscpd-rs
```

`make deps` also installs `jscpd-rs` automatically when the repository's
`.go-code-health.json` selects `"duplication_engine": "jscpd-rs"`.

The selected engine is recorded in `.go-code-health.json` and is included in
the analyzer's JSON report. If `jscpd-rs` is selected but `jscpd` is not
installed, the analyzer reports an explicit setup error rather than silently
using a different engine.

On the first run, if the file is absent, the skill performs a full scan,
shows the baseline, asks the user to choose the thresholds, and creates the
file in the project repository. It must run a full scan before every coding
task and again after changes. A task passes only when the scan is complete and
the overall score and every configured dimension meet their thresholds. If an
existing policy fails, code-health remediation takes priority and the agent
must update the code toward the configured values before unrelated work.

See [the project policy reference](chnq/references/project-config.md) for the
complete schema, first-run behavior, and a concrete threshold example.

Run the bundled tests:

```sh
make test
```

The top-level Makefile targets the `chnq` project by default. To scan the
current project with the compiled analyzer:

```sh
make run TARGET=/path/to/your/go/repo FORMAT=text
```

To pass analyzer options through Make:

```sh
make run TARGET=/path/to/your/go/repo ARGS="--fail-under 7"
```

### What it measures

- Go AST branching and nesting complexity
- Oversized functions and files
- Exact repeated 35-token blocks across functions
- Go-module-local package coupling
- Exported API documentation coverage
- Discarded return values and `panic` usage
- TODO/FIXME markers and concurrency operations
- Package fan-in, dependency depth, and import cycles
- Optional Git changed-line hotspots (`--include-git-churn`)

Scores range from 0.0 to 10.0. The model is a custom heuristic and is not
equivalent to SonarQube, CodeScene, or an ISO certification.

The additional health metrics are reported as measurements and findings; they
are not folded into the four-dimensional score. Git churn is opt-in because it
depends on repository history and is not a source-only signal. The analyzer
does not measure test correctness, test coverage, security, type errors,
cohesion, architecture layer violations, or semantic clones. It never executes
code from the repository under analysis.

### How metrics are calculated

The analyzer produces scores from **0.0 to 10.0**, where higher is better.
The model uses these weights:

- **Complexity: 40%**
- **Structure: 25%**
- **Duplication: 20%**
- **Coupling: 15%**

The overall score is the weighted average of the available metrics. If coupling
is unavailable because the project has no `go.mod`, the other weights are
renormalized. Scores are rounded to one decimal place in the report.

For policy configuration, see the simple engineer-facing examples in
[the project policy reference](chnq/references/project-config.md).

#### Complexity

Complexity estimates how difficult a function's logic is to follow. The
analyzer parses the Go syntax tree and adds points for `if`, `for`, `range`,
`switch`, type switch, and `select` statements, extra nesting, explicit
`case` clauses, and boolean `&&` or `||` operators.

Function scores are assigned from branching complexity:

```text
0–5   = 10
6–10  = 8
11–15 = 6
16–25 = 4
26–40 = 2
>40   = 0
```

The file score is an 80% function-length-weighted average and a 20%
contribution from the file's lowest-scoring function.

#### Structure

Structure estimates whether the code is divided into manageable files and
functions. The analyzer penalizes functions longer than 40 lines, nesting
deeper than three levels, and files longer than 400 lines.

The function penalty is:

```text
min(10, 0.08 × max(0, function_lines − 40)
       + 1.25 × max(0, nesting_depth − 3))
```

The file score starts at 10 and subtracts a weighted average of function
penalties, the worst function penalty, and a file-length penalty. The
file-length penalty is capped at 2.5 points.

#### Duplication

Duplication estimates how much code is repeated between functions. The
analyzer removes comments and automatically inserted semicolons, keeps exact
identifier and literal spellings, and finds repeated contiguous windows of 35
lexical tokens in different functions. Overlapping matches count only once per
function.

```text
duplication_percent = cloned_function_tokens / total_function_tokens × 100
duplication_score = clamp(10 − duplication_percent / 10, 0, 10)
```

This does not detect renamed-variable clones, semantic duplication, or
repetition shorter than 35 tokens.

#### Coupling

Coupling estimates how difficult it is to change a package independently. It
counts distinct imports of other packages within the same Go module, excluding
standard-library and third-party imports.

```text
coupling_score = clamp(10 − 1.25 × max(0, internal_imports − 4), 0, 10)
```

For example, if a package imports these internal packages:

```text
example.com/store/catalog
example.com/store/payments
example.com/store/users
```

its internal package fan-out is 3. The coupling score is 10 because the first
four internal dependencies do not reduce the score. With five internal
dependencies, the score becomes 8.75; with eight, it becomes 5.0.

The analyzer calculates this separately for each package directory. Every file
in that package receives the package's coupling score, and package/repository
scores are then weighted by source lines. It counts distinct imported package
paths, not the number of import statements or the number of symbols used.

Coupling is unavailable when no `go.mod` can be found in the target or its
parents. The analyzer does not search child directories for nested modules, so
scan a Go module directory directly when the repository contains multiple
modules.

Coupling is a simple dependency-count signal. It does not measure reverse
dependencies, symbol-level usage, interface quality, architecture boundaries,
or whether a dependency is easy to replace.

#### Additional health metrics

The JSON report includes a `health` object at repository and file level:

- `exported_symbols` and `documentation_percent` measure public API
  documentation coverage.
- `ignored_return_values` and `panics` identify reliability review points.
- `todos`, `fixmes`, `goroutines`, and `channel_operations` provide
  maintainability and concurrency indicators.
- `incoming_dependencies`, `dependency_depth`, and `import_cycles` describe
  package graph structure.
- `git_churn` is populated only with `--include-git-churn`; `git_hotspots`
  contains the five files with the most changed lines.

These are deliberately exposed as raw measurements and findings rather than
new score dimensions, so existing score thresholds remain comparable across
scans. Findings include undocumented exports, discarded return values, panic
usage, import cycles, and high Git churn.

#### Aggregation

File scores are combined into package and repository scores using source lines
as weights. The analyzer also reports individual dimension averages, package
scores, file scores, and up to five lowest-scoring files below 8.0 as
hotspots.

#### God-object review

The skill also performs a qualitative god-object review for hotspots and
packages with weak structure scores. It inspects Go structs and their methods
for one type that owns several unrelated responsibilities, such as I/O,
parsing, business rules, and reporting.

This is a design observation, not a numeric analyzer metric. The skill reports
the type, file, line, responsibilities, and evidence. A large type or a type
with many methods is not automatically a god object; the responsibilities must
be meaningfully unrelated.

#### Mixed-responsibility review

The skill also reviews hotspot files and packages for unrelated concerns
grouped together. Examples include a package that performs file or network
I/O, parses data, applies business rules, persists results, formats output,
and owns test assertions.

The skill reports:

- The file or package with mixed responsibilities
- Each responsibility found
- The code evidence supporting the observation
- A possible focused split, such as moving parsing, persistence, or reporting
  into separate components

This is also a qualitative design observation. A file or package is not a
problem merely because it contains several functions; the responsibilities
must be meaningfully unrelated.

### Install as an agent skill

Copy the entire `chnq` folder, not just `SKILL.md`, into the agent's skills
directory. Common options include:

- Codex (project): `<repo>/.agents/skills/go-code-health/`
- Codex (user): `~/.agents/skills/go-code-health/`
- Claude Code (project): `<repo>/.claude/skills/go-code-health/`
- Claude Code (user): `~/.claude/skills/go-code-health/`

The Makefile can install it for Claude Code:

```sh
make install-claude
```

For Codex, install it in the user skills directory:

```sh
make install-codex
```

For GitHub Copilot Chat, install it as a repository skill under
`.github/skills`:

```sh
make install-github
```

Then use Copilot Chat in Agent mode and ask:

```text
Use the go-code-health skill to analyze this repository.
```

For a single project, override the destination:

```sh
make install-claude CLAUDE_SKILLS_DIR=/path/to/project/.claude/skills
make install-codex CODEX_SKILLS_DIR=/path/to/project/.agents/skills
make install-github GITHUB_SKILLS_DIR=/path/to/project/.github/skills
```

Then ask the agent:

> Use go-code-health to score this repository and explain the worst three files.

The skill instructions tell the agent to run the bundled command and use its
actual JSON report instead of guessing a score.

### Repository layout

```text
skills/
├── Makefile
├── README.md
└── chnq/
    ├── SKILL.md
    ├── go.mod
    ├── cmd/go-code-health/
    ├── scripts/
    ├── references/
    └── examples/
```

`chnq` is an initial model and is not yet calibrated against independent human
reviews of real Go code. Before using the score for PR blocking across teams,
validate thresholds against varied Go services and manually rated examples.
