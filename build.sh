#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

base_version="dev"
short_sha="nogit"
dirty_suffix=""
build_timestamp="$(date -u +%Y%m%d.%H%M)"

if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  if tag="$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null)"; then
    base_version="$tag"
  fi

  if sha="$(git rev-parse --short HEAD 2>/dev/null)"; then
    short_sha="$sha"
  fi

  if ! git diff --quiet --ignore-submodules HEAD -- 2>/dev/null; then
    dirty_suffix=".dirty"
  fi
fi

version="${base_version}+${build_timestamp}.sha${short_sha}${dirty_suffix}"
ldflags="-X github.com/jonathanhecl/vibe-coder/internal/version.Value=${version}"

echo "[INFO] Building vibe-coder ${version}"
go build -ldflags "$ldflags" -o vibe-coder ./cmd/vibe-coder
echo "[OK] Built ./vibe-coder"
