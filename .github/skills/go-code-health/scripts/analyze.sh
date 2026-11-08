#!/usr/bin/env sh
# Build and execute the read-only Go code health analyzer, preserving exit codes.
set -eu
SKILL_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if ! command -v go >/dev/null 2>&1; then
  echo 'go-code-health requires Go 1.27.3+ (or automatic Go toolchain download) in PATH' >&2
  exit 2
fi
TARGET=.
case "${1:-}" in
  ""|-*) ;;
  *) TARGET=$1; shift ;;
esac
case "$TARGET" in
  /*) ;;
  *) TARGET="$PWD/$TARGET" ;;
esac
BUILD_DIR=$(mktemp -d)
trap 'rm -rf "$BUILD_DIR"' EXIT
(cd "$SKILL_ROOT" && GOWORK=off go build -o "$BUILD_DIR/go-code-health" ./cmd/go-code-health)
"$BUILD_DIR/go-code-health" --path "$TARGET" "$@"
