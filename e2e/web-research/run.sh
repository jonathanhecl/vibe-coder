#!/usr/bin/env bash
# End-to-end web-research test: the agent must search the web, fetch real
# pages, and write a sourced Markdown report. Exercises WebSearch and WebFetch
# together, including their parsing of real search-result markup.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$DIR/../.." && pwd)"

MODEL="${MODEL:-baytout3/Gemma-4-Uncensored-HauhauCS-Aggressive:e4b}"
OLLAMA_HOST="${OLLAMA_HOST:-http://127.0.0.1:11434}"
TIMEOUT_SECS="${TIMEOUT_SECS:-1800}"
MAX_TOKENS="${MAX_TOKENS:-8192}"
EXTRA_FLAGS="${EXTRA_FLAGS:---no-think}"
TOPIC="${TOPIC:-the Go programming language: its origin, creators, and design goals}"

WORK="$DIR/work"
rm -rf "$WORK"
mkdir -p "$WORK/home"

echo "== building vibe =="
(cd "$ROOT" && go build -o "$WORK/vibe" ./cmd/vibe)

echo "== research topic: ${TOPIC} =="
PROMPT="Research ${TOPIC} using WebSearch and WebFetch, then write the sourced report to report.md exactly as the pinned guide describes."

echo "== running vibe (model=${MODEL}, timeout=${TIMEOUT_SECS}s) =="
set +e
(
  cd "$WORK"
  HOME="$WORK/home" ./vibe \
    --model "$MODEL" \
    --ollama-host "$OLLAMA_HOST" \
    --max-tokens "$MAX_TOKENS" \
    -y $EXTRA_FLAGS \
    --context "$DIR/context.md" \
    -p "$PROMPT"
) >"$WORK/run.log" 2>&1 &
AGENT_PID=$!
( sleep "$TIMEOUT_SECS"; kill -9 "$AGENT_PID" 2>/dev/null ) &
WATCHER=$!
wait "$AGENT_PID" && AGENT_RC=0 || AGENT_RC=$?
kill "$WATCHER" 2>/dev/null || true
set -e

# Count actual tool cards (hammer prefix), not mentions in the model's prose.
SEARCHES=$(grep -c '🔨 WebSearch' "$WORK/run.log" || true)
FETCHES=$(grep -c '🔨 WebFetch' "$WORK/run.log" || true)
echo "tool cards: WebSearch=${SEARCHES} WebFetch=${FETCHES}"

echo
echo "=========== report.md ==========="
if [ -f "$WORK/report.md" ]; then
  cat "$WORK/report.md"
else
  echo "(no report.md was written)"
fi
echo "================================="
echo

echo "== verifying report =="
set +e
(cd "$ROOT" && go run ./e2e/web-research/checker -dir "$WORK")
CHECK_RC=$?
set -e

echo
echo "agent_exit=${AGENT_RC} search_cards=${SEARCHES} fetch_cards=${FETCHES} check_exit=${CHECK_RC} workspace=${WORK}"
[ "$CHECK_RC" -eq 0 ]
