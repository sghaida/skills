# Go Code Health Skill - build, analysis and Claude Code installation.
# Run these targets from the repository root.
SHELL := /bin/sh

GO ?= go
ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
PROJECT_DIR ?= $(ROOT)/chnq
BUILD_DIR ?= $(PROJECT_DIR)/.build
BIN := $(BUILD_DIR)/go-code-health
TARGET ?= $(PROJECT_DIR)
FORMAT ?= json
ARGS ?=
CLAUDE_SKILLS_DIR ?= $(HOME)/.claude/skills
CLAUDE_SKILL_DIR := $(CLAUDE_SKILLS_DIR)/go-code-health

.PHONY: help deps install-deps build run cmd test vet install-claude clean

help:
	@printf '%s\n' \
	  'make deps             Download Go module dependencies (none currently)' \
	  'make install-deps     Alias for make deps' \
	  'make build            Build chnq/.build/go-code-health' \
	  'make run              Build and run scanner (TARGET=chnq FORMAT=json ARGS="")' \
	  'make cmd              Run scanner using go run (without saving a binary)' \
	  'make test             Run all Go tests' \
	  'make vet              Run go vet' \
	  'make install-claude   Install the skill to ~/.claude/skills/go-code-health' \
	  'make clean            Remove local build output' \
	  '' \
	  'Examples:' \
	  '  make run TARGET=/path/to/repo FORMAT=text' \
	  '  make run TARGET=/path/to/repo ARGS="--fail-under 7"' \
	  '  make cmd TARGET=/path/to/repo FORMAT=json' \
	  '  make install-claude CLAUDE_SKILLS_DIR=/path/to/project/.claude/skills' \
	  '' \
	  'Override PROJECT_DIR when the Go project is in a different directory.'

deps:
	@cd "$(PROJECT_DIR)" && GOWORK=off $(GO) mod download

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

test:
	@cd "$(PROJECT_DIR)" && GOWORK=off $(GO) test ./...

vet:
	@cd "$(PROJECT_DIR)" && GOWORK=off $(GO) vet ./...

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

clean:
	@rm -rf "$(BUILD_DIR)"
