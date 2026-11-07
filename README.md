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

Scores range from 0.0 to 10.0. The model is a custom heuristic and is not
equivalent to SonarQube, CodeScene, or an ISO certification.

It does not measure test correctness, test coverage, security, type errors, Git
churn, cohesion, architecture layer violations, or semantic clones. It never
executes code from the repository under analysis.

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

For a single project, override the destination:

```sh
make install-claude CLAUDE_SKILLS_DIR=/path/to/project/.claude/skills
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
