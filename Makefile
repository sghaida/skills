# Go Code Health Skill - build, analysis and Claude Code installation.
# Run these targets from the repository root.
SHELL := /bin/sh

GO ?= go
GOLANGCI_LINT ?= $(shell "$(GO)" env GOPATH)/bin/golangci-lint
GOLANGCI_LINT_CONFIG ?= $(ROOT)/.golangci.yml
NPM ?= npm
NPM_PREFIX ?= $(HOME)/.local
ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
PROJECT_DIR ?= $(ROOT)/chnq
BUILD_DIR ?= $(PROJECT_DIR)/.build
BIN := $(BUILD_DIR)/go-code-health
TARGET ?= $(PROJECT_DIR)
FORMAT ?= json
ARGS ?=
-include $(ROOT)/.golint.config
CLAUDE_SKILLS_DIR ?= $(HOME)/.claude/skills
CLAUDE_SKILL_DIR := $(CLAUDE_SKILLS_DIR)/go-code-health

.PHONY: help deps install-deps build run cmd fmt tidy test test-cover coverage vet install-golangci-lint lint precheck install-hooks install-claude install-codex install-github install-jscpd-rs clean

help:
	@printf '%s\n' \
	  'make deps             Download Go dependencies and install configured tools' \
	  'make install-deps     Alias for make deps' \
	  'make build            Build chnq/.build/go-code-health' \
	  'make run              Build and run scanner (TARGET=chnq FORMAT=json ARGS="")' \
	  'make cmd              Run scanner using go run (without saving a binary)' \
	  'make fmt              Format all Go source files' \
	  'make tidy             Run go mod tidy' \
	  'make test             Run all Go tests' \
	  'make test-cover       Run tests and report production-file coverage' \
	  'make coverage         Run tests and require 90% coverage per Go source file' \
	  'make vet              Run go vet' \
	  'make precheck         Run formatting, tidy, tests, vet, and lint' \
	  'make lint             Install latest golangci-lint and run it' \
	  'make install-hooks    Configure the repository pre-commit hook' \
	  'make install-claude   Install the skill to ~/.claude/skills/go-code-health' \
	  'make install-codex    Install the skill to ~/.agents/skills/go-code-health' \
	  'make install-github   Install the skill to .github/skills/go-code-health' \
	  'make install-jscpd-rs Install the optional jscpd-rs duplication engine' \
	  'make clean            Remove local build output' \
	  '' \
	  'Examples:' \
	  '  make run TARGET=/path/to/repo FORMAT=text' \
	  '  make run TARGET=/path/to/repo ARGS="--fail-under 7"' \
	  '  make cmd TARGET=/path/to/repo FORMAT=json' \
	  '  make install-claude CLAUDE_SKILLS_DIR=/path/to/project/.claude/skills' \
	  '  make install-codex CODEX_SKILLS_DIR=/path/to/project/.agents/skills' \
	  '  make install-github GITHUB_SKILLS_DIR=/path/to/project/.github/skills' \
	  '' \
	  'Override PROJECT_DIR when the Go project is in a different directory.'

deps:
	@cd "$(PROJECT_DIR)" && GOWORK=off $(GO) mod download
	@"$(MAKE)" install-golangci-lint
	@if [ -f "$(ROOT)/.go-code-health.json" ] && \
	  grep -Eq '"duplication_engine"[[:space:]]*:[[:space:]]*"jscpd-rs"' "$(ROOT)/.go-code-health.json"; then \
	  "$(MAKE)" install-jscpd-rs; \
	fi

install-deps: deps

build:
	@mkdir -p "$(BUILD_DIR)"
	@cd "$(PROJECT_DIR)" && GOWORK=off $(GO) build -o "$(BIN)" ./cmd/go-code-health

run: build
	@"$(BIN)" --path "$(TARGET)" --format "$(FORMAT)" $(ARGS)

# For the exact scanner exit code, run the compiled binary or scripts/analyze.sh.
# Make reports any failing recipe as its own nonzero exit status.
cmd:
	@cd "$(PROJECT_DIR)" && GOWORK=off $(GO) run ./cmd/go-code-health --path "$(TARGET)" --format "$(FORMAT)" $(ARGS)

fmt:
	@find "$(PROJECT_DIR)" -type f -name '*.go' -not -path '*/.build/*' -print0 | xargs -0 gofmt -w

tidy:
	@cd "$(PROJECT_DIR)" && GOWORK=off $(GO) mod tidy

test:
	@cd "$(PROJECT_DIR)" && GOWORK=off $(GO) test ./...

test-cover:
	@set -eu; \
	profile=$$(mktemp "$${TMPDIR:-/tmp}/go-code-health-cover.XXXXXX"); \
	trap 'rm -f "$$profile"' EXIT INT TERM; \
	(cd "$(PROJECT_DIR)" && GOWORK=off $(GO) test ./... -coverprofile="$$profile"); \
	awk 'NR == 1 { next } { split($$1, location, ":"); file = location[1]; if (file ~ /_test\.go$$/) next; total[file] += $$2; if ($$3 > 0) covered[file] += $$2 } END { failed = 0; for (file in total) { percentage = 100 * covered[file] / total[file]; printf "%6.1f%% %s\n", percentage, file; if (percentage < 90) failed = 1 } exit failed }' "$$profile"

coverage: test-cover

vet:
	@cd "$(PROJECT_DIR)" && GOWORK=off $(GO) vet ./...

install-golangci-lint:
	@GOWORK=off GOTOOLCHAIN=go1.27.3+auto "$(GO)" install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	@test -x "$(GOLANGCI_LINT)" || { printf 'golangci-lint was not installed at %s\n' "$(GOLANGCI_LINT)" >&2; exit 2; }

lint: install-golangci-lint
	@cd "$(PROJECT_DIR)" && GOWORK=off "$(GOLANGCI_LINT)" run --config "$(GOLANGCI_LINT_CONFIG)" ./...

precheck: fmt tidy test vet lint

install-hooks:
	@git -C "$(ROOT)" config core.hooksPath .githooks
	@printf 'Configured Git hooks path: .githooks\n'

# Copy source only, never compiled binaries or local caches. Destination is configurable
# so this can install to either the user or project Claude Code skills directory.
install-claude:
	@set -eu; \
	  mkdir -p "$(CLAUDE_SKILL_DIR)"; \
	  if [ "$$(cd "$(PROJECT_DIR)" && pwd -P)" = "$$(cd "$(CLAUDE_SKILL_DIR)" && pwd -P)" ]; then \
	    printf 'Skill is already installed at: %s\n' "$(CLAUDE_SKILL_DIR)"; \
	    exit 0; \
	  fi; \
	  cp "$(PROJECT_DIR)/SKILL.md" "$(ROOT)/README.md" "$(PROJECT_DIR)/go.mod" "$(ROOT)/Makefile" "$(CLAUDE_SKILL_DIR)/"; \
	  for dir in cmd scripts references examples; do \
	    cp -R "$(PROJECT_DIR)/$$dir" "$(CLAUDE_SKILL_DIR)/"; \
	  done; \
	  printf 'Installed Go Code Health skill to: %s\n' "$(CLAUDE_SKILL_DIR)"

CODEX_SKILLS_DIR ?= $(HOME)/.agents/skills
CODEX_SKILL_DIR := $(CODEX_SKILLS_DIR)/go-code-health

install-codex:
	@set -eu; \
	  mkdir -p "$(CODEX_SKILL_DIR)"; \
	  if [ "$$(cd "$(PROJECT_DIR)" && pwd -P)" = "$$(cd "$(CODEX_SKILL_DIR)" && pwd -P)" ]; then \
	    printf 'Skill is already installed at: %s\n' "$(CODEX_SKILL_DIR)"; \
	    exit 0; \
	  fi; \
	  cp "$(PROJECT_DIR)/SKILL.md" "$(ROOT)/README.md" "$(PROJECT_DIR)/go.mod" "$(ROOT)/Makefile" "$(CODEX_SKILL_DIR)/"; \
	  for dir in cmd scripts references examples; do \
	    cp -R "$(PROJECT_DIR)/$$dir" "$(CODEX_SKILL_DIR)/"; \
	  done; \
	  printf 'Installed Go Code Health skill to: %s\n' "$(CODEX_SKILL_DIR)"

GITHUB_SKILLS_DIR ?= $(ROOT)/.github/skills
GITHUB_SKILL_DIR := $(GITHUB_SKILLS_DIR)/go-code-health

install-github:
	@set -eu; \
	  mkdir -p "$(GITHUB_SKILL_DIR)"; \
	  if [ "$$(cd "$(PROJECT_DIR)" && pwd -P)" = "$$(cd "$(GITHUB_SKILL_DIR)" && pwd -P)" ]; then \
	    printf 'Skill is already installed at: %s\n' "$(GITHUB_SKILL_DIR)"; \
	    exit 0; \
	  fi; \
	  cp "$(PROJECT_DIR)/SKILL.md" "$(PROJECT_DIR)/go.mod" "$(GITHUB_SKILL_DIR)/"; \
	  for dir in cmd scripts references examples; do \
	    cp -R "$(PROJECT_DIR)/$$dir" "$(GITHUB_SKILL_DIR)/"; \
	  done; \
	  printf 'Installed Go Code Health skill to: %s\n' "$(GITHUB_SKILL_DIR)"

install-jscpd-rs:
	@command -v "$(NPM)" >/dev/null 2>&1 || { printf 'npm is required to install jscpd-rs\n' >&2; exit 2; }
	@"$(NPM)" install --prefix "$(NPM_PREFIX)" jscpd-rs
	@printf 'Installed jscpd-rs. Ensure %s/bin is on PATH before running scans.\n' "$(NPM_PREFIX)"

clean:
	@rm -rf "$(BUILD_DIR)"
