#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "Running GoReleaser snapshot..."
if command -v goreleaser >/dev/null 2>&1; then
  goreleaser release --snapshot --clean --skip=publish
else
  go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=publish
fi

echo "Snapshot artifacts generated under ./dist"

