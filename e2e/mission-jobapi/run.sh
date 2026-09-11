#!/usr/bin/env bash
# End-to-end mission test: the agent must process dataset.jsonl through a local
# async job API, retry transient failures, and record only verified results.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$DIR/../.." && pwd)"

MODEL="${MODEL:-baytout3/Gemma-4-Uncensored-HauhauCS-Aggressive:e4b}"
OLLAMA_HOST="${OLLAMA_HOST:-http://127.0.0.1:11434}"
PORT="${PORT:-8799}"
POLL_TICKS="${POLL_TICKS:-2}"
FAIL_EVERY="${FAIL_EVERY:-3}"
TIMEOUT_SECS="${TIMEOUT_SECS:-1800}"
MAX_TOKENS="${MAX_TOKENS:-8192}"
EXTRA_FLAGS="${EXTRA_FLAGS:---no-think}"

DATASET="${DATASET:-$DIR/dataset.jsonl}"

WORK="$DIR/work"
rm -rf "$WORK"
mkdir -p "$WORK"
cp "$DATASET" "$WORK/dataset.jsonl"
sed "s#127.0.0.1:8799#127.0.0.1:${PORT}#g" "$DIR/mission-context.md" > "$WORK/mission-context.md"

echo "== building vibe =="
(cd "$ROOT" && go build -o "$WORK/vibe" ./cmd/vibe)

echo "== starting job API on 127.0.0.1:${PORT} (poll-ticks=${POLL_TICKS}, fail-every=${FAIL_EVERY}) =="
go run "$DIR/server.go" -addr "127.0.0.1:${PORT}" -poll-ticks "$POLL_TICKS" -fail-every "$FAIL_EVERY" >"$WORK/server.log" 2>&1 &
SERVER_PID=$!
cleanup() { kill "$SERVER_PID" 2>/dev/null || true; }
trap cleanup EXIT

for _ in $(seq 1 20); do
  if curl -s "http://127.0.0.1:${PORT}/_stats" >/dev/null 2>&1; then break; fi
  sleep 0.25
done

PROMPT="Run the pinned mission guide end to end: process every item in dataset.jsonl through the local job API at http://127.0.0.1:${PORT}, retry transient job failures, verify each result against the expect field, and record only verified results in results.jsonl. Start a mission with MissionStart so you keep working autonomously, and call MissionComplete only when every item is verified."

echo "== running vibe (model=${MODEL}, timeout=${TIMEOUT_SECS}s) =="
run_with_timeout() {
  local secs="$1"; shift
  "$@" &
  local pid=$!
  ( sleep "$secs"; kill "$pid" 2>/dev/null ) &
  local watcher=$!
  wait "$pid"; local rc=$?
  kill "$watcher" 2>/dev/null || true
  return $rc
}

set +e
(
  cd "$WORK"
  run_with_timeout "$TIMEOUT_SECS" ./vibe \
    --model "$MODEL" \
    --ollama-host "$OLLAMA_HOST" \
    --max-tokens "$MAX_TOKENS" \
    -y $EXTRA_FLAGS \
    --context "$WORK/mission-context.md" \
    -p "$PROMPT"
)
AGENT_RC=$?
set -e

echo
echo "== job API stats =="
curl -s "http://127.0.0.1:${PORT}/_stats" || true
echo

echo "== verifying results =="
set +e
(cd "$ROOT" && go run ./e2e/mission-jobapi/checker -dir "$WORK")
CHECK_RC=$?
set -e

echo
echo "agent_exit=${AGENT_RC} check_exit=${CHECK_RC} workspace=${WORK}"
[ "$CHECK_RC" -eq 0 ]
