#!/usr/bin/env sh
set -eu

REPOSITORY="${GO_CODE_HEALTH_REPOSITORY:-sghaida/skills}"
VERSION="${GO_CODE_HEALTH_VERSION:-latest}"
TARGET=""
SCOPE=""

usage() {
  printf '%s\n' \
    'Usage: ./install.sh [--target claude|codex|copilot] [--scope user|project]' \
    '' \
    'Downloads the matching release archive and installs the go-code-health skill.' \
    'Defaults to interactive prompts when target or scope is omitted.'
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --target)
      [ "$#" -ge 2 ] || { printf '%s\n' '--target requires a value' >&2; exit 2; }
      TARGET=$2
      shift 2
      ;;
    --scope)
      [ "$#" -ge 2 ] || { printf '%s\n' '--scope requires a value' >&2; exit 2; }
      SCOPE=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'Unknown option: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$(uname -s):$(uname -m)" in
  Darwin:arm64) PLATFORM=darwin-arm64 ;;
  Darwin:x86_64) PLATFORM=darwin-amd64 ;;
  Linux:x86_64) PLATFORM=linux-amd64 ;;
  Linux:aarch64|Linux:arm64) PLATFORM=linux-arm64 ;;
  *)
    printf 'Unsupported platform: %s/%s\n' "$(uname -s)" "$(uname -m)" >&2
    exit 2
    ;;
esac

case "$TARGET" in
  "") ;;
  claude|codex|copilot) ;;
  *) printf 'Unsupported target: %s\n' "$TARGET" >&2; exit 2 ;;
esac

if [ -z "$TARGET" ]; then
  printf '%s\n' 'Install for:'
  printf '%s\n' '  1) Claude'
  printf '%s\n' '  2) Codex'
  printf '%s\n' '  3) Copilot/GitHub'
  printf 'Choose [1-3]: '
  read -r choice
  case "$choice" in
    1) TARGET=claude ;;
    2) TARGET=codex ;;
    3) TARGET=copilot ;;
    *) printf '%s\n' 'Invalid target.' >&2; exit 2 ;;
  esac
fi

if [ -z "$SCOPE" ]; then
  printf '%s\n' 'Install scope:'
  printf '%s\n' '  1) User/local environment'
  printf '%s\n' '  2) Current project'
  printf 'Choose [1-2]: '
  read -r choice
  case "$choice" in
    1) SCOPE=user ;;
    2) SCOPE=project ;;
    *) printf '%s\n' 'Invalid scope.' >&2; exit 2 ;;
  esac
fi

case "$SCOPE" in
  user|project) ;;
  *) printf 'Unsupported scope: %s\n' "$SCOPE" >&2; exit 2 ;;
esac

if [ "$SCOPE" = project ]; then
  case "$TARGET" in
    claude) DEST="$PWD/.claude/skills/go-code-health" ;;
    codex) DEST="$PWD/.agents/skills/go-code-health" ;;
    copilot) DEST="$PWD/.github/skills/go-code-health" ;;
  esac
else
  case "$TARGET" in
    claude) DEST="$HOME/.claude/skills/go-code-health" ;;
    codex) DEST="$HOME/.agents/skills/go-code-health" ;;
    copilot) DEST="$HOME/.copilot/skills/go-code-health" ;;
  esac
fi

command -v curl >/dev/null 2>&1 || {
  printf '%s\n' 'curl is required to install the skill.' >&2
  exit 2
}
command -v tar >/dev/null 2>&1 || {
  printf '%s\n' 'tar is required to install the skill.' >&2
  exit 2
}

if [ "$VERSION" = latest ]; then
  VERSION=$(curl --fail --location --silent --show-error \
    "https://api.github.com/repos/$REPOSITORY/releases/latest" |
    sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
  [ -n "$VERSION" ] || { printf '%s\n' 'Could not determine the latest release tag.' >&2; exit 2; }
fi
BASE_URL="https://github.com/$REPOSITORY/releases/download/$VERSION"
ARCHIVE="go-code-health-$VERSION-$PLATFORM.tar.gz"

TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT INT TERM
curl --fail --location --silent --show-error \
  "$BASE_URL/$ARCHIVE" \
  --output "$TEMP_DIR/$ARCHIVE"
tar -xzf "$TEMP_DIR/$ARCHIVE" -C "$TEMP_DIR"

PACKAGE_DIR=$(find "$TEMP_DIR" -mindepth 1 -maxdepth 1 -type d -name 'go-code-health-*' -print -quit)
[ -n "$PACKAGE_DIR" ] || { printf '%s\n' 'Release archive has an invalid layout.' >&2; exit 2; }

mkdir -p "$DEST"
cp -R "$PACKAGE_DIR/." "$DEST/"
chmod +x "$DEST/bin/go-code-health" "$DEST/scripts/analyze.sh"
printf 'Installed go-code-health for %s at %s\n' "$TARGET" "$DEST"
