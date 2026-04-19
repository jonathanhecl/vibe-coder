#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "== POST-21: race test =="
if ! CGO_ENABLED=1 go test ./... -race -count=1; then
  echo "Race tests failed (or toolchain lacks CGO support)." >&2
  exit 1
fi

echo "== POST-21: coverage threshold =="
PROFILE="cover_post21.out"
go test ./internal/agent ./internal/session ./internal/ollama ./internal/tools ./internal/permissions -coverprofile="$PROFILE"

TOTAL_LINE="$(go tool cover -func="$PROFILE" | tail -n 1)"
TOTAL_PCT="$(echo "$TOTAL_LINE" | awk '{print $3}' | tr -d '%')"
echo "Coverage total: ${TOTAL_PCT}%"

awk -v pct="$TOTAL_PCT" 'BEGIN { exit (pct+0 >= 80.0) ? 0 : 1 }' || {
  echo "Coverage below 80%." >&2
  exit 1
}

echo "POST-21 gate passed."

