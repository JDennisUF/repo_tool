#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_FILE="$ROOT_DIR/internal/version/build.txt"
BINARY="${1:-rt}"

current_build="$(tr -d '[:space:]' < "$BUILD_FILE")"
if [[ -z "$current_build" ]]; then
  current_build=0
fi
if ! [[ "$current_build" =~ ^[0-9]+$ ]]; then
  echo "invalid build number in $BUILD_FILE: $current_build" >&2
  exit 1
fi

next_build=$((current_build + 1))
printf '%s\n' "$next_build" > "$BUILD_FILE"

go build -o "$ROOT_DIR/$BINARY" ./cmd/rt
printf 'Built %s with build %s\n' "$BINARY" "$next_build"
