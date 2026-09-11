#!/usr/bin/env bash
# End-to-end code-editing test: the agent must fix a small Go project's failing
# tests using minimal edits, without touching tests, config, or unrelated files.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$DIR/../.." && pwd)"

MODEL="${MODEL:-baytout3/Gemma-4-Uncensored-HauhauCS-Aggressive:e4b}"
OLLAMA_HOST="${OLLAMA_HOST:-http://127.0.0.1:11434}"
TIMEOUT_SECS="${TIMEOUT_SECS:-1800}"
MAX_TOKENS="${MAX_TOKENS:-8192}"
EXTRA_FLAGS="${EXTRA_FLAGS:---no-think}"

WORK="$DIR/work"
PROJECT="$WORK/project"
rm -rf "$WORK"
mkdir -p "$WORK/home"
cp -R "$DIR/fixture" "$PROJECT"
sed "s#__PROJECT_DIR__#${PROJECT}#g" "$DIR/context.md" > "$WORK/context.md"

# Baseline commit so the checker can see exactly what the agent changed.
git -C "$PROJECT" init -q
git -C "$PROJECT" add -A
git -C "$PROJECT" -c user.email=e2e@example.com -c user.name=e2e commit -q -m baseline

echo "== building vibe =="
(cd "$ROOT" && go build -o "$WORK/vibe" ./cmd/vibe)

PROMPT="The Go project in the working directory has failing tests. Run the tests, find the bugs in the non-test source files, fix them with minimal edits, and make the test suite pass. Do not modify the tests, go.mod, or NOTES.md."

echo "== running vibe (model=${MODEL}, timeout=${TIMEOUT_SECS}s) =="
set +e
(
  cd "$PROJECT"
  HOME="$WORK/home" "$WORK/vibe" \
    --model "$MODEL" \
    --ollama-host "$OLLAMA_HOST" \
    --max-tokens "$MAX_TOKENS" \
    -y $EXTRA_FLAGS \
    --context "$WORK/context.md" \
    -p "$PROMPT"
) >"$WORK/run.log" 2>&1 &
AGENT_PID=$!
( sleep "$TIMEOUT_SECS"; kill -9 "$AGENT_PID" 2>/dev/null ) &
WATCHER=$!
wait "$AGENT_PID" && AGENT_RC=0 || AGENT_RC=$?
kill "$WATCHER" 2>/dev/null || true
set -e

echo
echo "== changes made by the agent =="
git -C "$PROJECT" status --porcelain || true
echo "--- diff ---"
git -C "$PROJECT" --no-pager diff || true
echo "=============="
echo

echo "== verifying edits =="
set +e
(cd "$ROOT" && go run ./e2e/code-edit/checker -dir "$PROJECT")
CHECK_RC=$?
set -e

echo
echo "agent_exit=${AGENT_RC} check_exit=${CHECK_RC} workspace=${WORK}"
[ "$CHECK_RC" -eq 0 ]
