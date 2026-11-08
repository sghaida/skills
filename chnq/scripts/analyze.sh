#!/usr/bin/env sh
# Build and execute the read-only Go code health analyzer, preserving exit codes.
set -eu
SKILL_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TARGET=.
case "${1:-}" in
  ""|-*) ;;
  *) TARGET=$1; shift ;;
esac
case "$TARGET" in
  /*) ;;
  *) TARGET="$PWD/$TARGET" ;;
esac
if [ -x "$SKILL_ROOT/bin/go-code-health" ]; then
  exec "$SKILL_ROOT/bin/go-code-health" --path "$TARGET" "$@"
fi
if ! command -v go >/dev/null 2>&1; then
  echo 'go-code-health release packages include a binary; source checkouts require Go 1.27.3+.' >&2
  exit 2
fi
BUILD_DIR=$(mktemp -d)
trap 'rm -rf "$BUILD_DIR"' EXIT
(cd "$SKILL_ROOT" && GOWORK=off go build -o "$BUILD_DIR/go-code-health" ./cmd/go-code-health)
"$BUILD_DIR/go-code-health" --path "$TARGET" "$@"
