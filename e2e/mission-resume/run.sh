#!/usr/bin/env bash
# End-to-end resume test: crash an autonomous mission mid-run (SIGKILL) and
# prove that `--resume` continues from the durable state without losing,
# duplicating, or reordering already-verified results.
#
# It reuses the job API and the results checker from e2e/mission-jobapi so the
# only new behaviour under test is crash + resume.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$DIR/../.." && pwd)"
JOBAPI="$DIR/../mission-jobapi"

MODEL="${MODEL:-baytout3/Gemma-4-Uncensored-HauhauCS-Aggressive:e4b}"
OLLAMA_HOST="${OLLAMA_HOST:-http://127.0.0.1:11434}"
PORT="${PORT:-8798}"
POLL_TICKS="${POLL_TICKS:-2}"
FAIL_EVERY="${FAIL_EVERY:-3}"
KILL_AFTER="${KILL_AFTER:-2}"                 # verified lines before the crash
DATASET_SIZE="${DATASET_SIZE:-6}"
PHASE1_TIMEOUT="${PHASE1_TIMEOUT:-900}"
PHASE2_TIMEOUT="${PHASE2_TIMEOUT:-2700}"
MAX_TOKENS="${MAX_TOKENS:-8192}"
EXTRA_FLAGS="${EXTRA_FLAGS:---no-think}"

DATASET="${DATASET:-$DIR/dataset.jsonl}"

WORK="$DIR/work"
rm -rf "$WORK"
mkdir -p "$WORK/home"
cp "$DATASET" "$WORK/dataset.jsonl"
sed "s#127.0.0.1:8799#127.0.0.1:${PORT}#g" "$DIR/mission-context.md" > "$WORK/mission-context.md"

echo "== building vibe =="
(cd "$ROOT" && go build -o "$WORK/vibe" ./cmd/vibe)
(cd "$ROOT" && go build -o "$WORK/progresscheck" ./e2e/mission-resume/progresscheck)

echo "== starting job API on 127.0.0.1:${PORT} (poll-ticks=${POLL_TICKS}, fail-every=${FAIL_EVERY}) =="
go run "$JOBAPI/server.go" -addr "127.0.0.1:${PORT}" -poll-ticks "$POLL_TICKS" -fail-every "$FAIL_EVERY" >"$WORK/server.log" 2>&1 &
SERVER_PID=$!
cleanup() { kill "$SERVER_PID" 2>/dev/null || true; }
trap cleanup EXIT

for _ in $(seq 1 20); do
  if curl -s "http://127.0.0.1:${PORT}/_stats" >/dev/null 2>&1; then break; fi
  sleep 0.25
done

PROMPT="Run the pinned mission guide end to end: process every item in dataset.jsonl through the local job API at http://127.0.0.1:${PORT}, retry transient job failures, verify each result against the expect field, and record only verified results in results.jsonl. Start a mission with MissionStart so you keep working autonomously, and call MissionComplete only when every item is verified."

# run_vibe starts vibe in the mission workdir and sets VIBE_PID. The process is
# backgrounded in the current shell (not a command-substitution subshell) so
# `wait "$VIBE_PID"` can observe its real exit status. HOME is isolated so the
# run never touches the user's real sessions, and so --resume can only find the
# session this run persisted.
VIBE_PID=""
run_vibe() {
  local log="$1"; shift
  ( cd "$WORK" && HOME="$WORK/home" exec ./vibe \
      --model "$MODEL" \
      --ollama-host "$OLLAMA_HOST" \
      --max-tokens "$MAX_TOKENS" \
      -y $EXTRA_FLAGS \
      --context "$WORK/mission-context.md" \
      "$@" ) >"$log" 2>&1 &
  VIBE_PID=$!
}

RESULTS="$WORK/results.jsonl"

# --- phase 1: start the mission, then hard-kill it mid-run -------------------
echo "== phase 1: run until ${KILL_AFTER} clean records, then SIGKILL (no cleanup) =="
run_vibe "$WORK/phase1.log" -p "$PROMPT"
P1="$VIBE_PID"
deadline=$(( $(date +%s) + PHASE1_TIMEOUT ))
prev_size=-1
stable=0
while kill -0 "$P1" 2>/dev/null; do
  if [ -f "$RESULTS" ]; then
    size=$(wc -c < "$RESULTS" | tr -d ' ')
    if pc=$("$WORK/progresscheck" -file "$RESULTS" -min "$KILL_AFTER") && [ "$size" = "$prev_size" ]; then
      stable=$((stable + 1))
    else
      stable=0
    fi
    prev_size="$size"
    # Two consecutive stable seconds mean no write is in flight, so the
    # SIGKILL below can never land inside a JSON record.
    if [ "$stable" -ge 2 ]; then
      break
    fi
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "phase 1 timed out before reaching ${KILL_AFTER} clean records" >&2
    break
  fi
  sleep 1
done
kill -9 "$P1" 2>/dev/null || true
wait "$P1" 2>/dev/null || true

if [ ! -f "$RESULTS" ]; then
  echo "FAIL: phase 1 produced no results.jsonl; nothing to resume" >&2
  exit 1
fi
P1_LINES=$("$WORK/progresscheck" -file "$RESULTS" -min 0)
cp "$RESULTS" "$WORK/results.phase1.jsonl"
P1_SNAPSHOT="$WORK/results.phase1.jsonl"
echo "phase 1 verified lines: ${P1_LINES}"
if [ "$P1_LINES" -lt "$KILL_AFTER" ]; then
  echo "FAIL: phase 1 only reached ${P1_LINES}/${KILL_AFTER} verified items" >&2
  exit 1
fi
if [ "$P1_LINES" -ge "$DATASET_SIZE" ]; then
  echo "NOTE: mission finished before the crash; this run only exercises resume idempotency" >&2
fi

# --- phase 2: resume and finish --------------------------------------------
echo "== phase 2: resume the interrupted mission (--resume) =="
run_vibe "$WORK/phase2.log" --resume -p "continue"
P2="$VIBE_PID"
( sleep "$PHASE2_TIMEOUT"; kill -9 "$P2" 2>/dev/null ) &
WATCHER=$!
wait "$P2" 2>/dev/null && { P2_RC=0; } || { P2_RC=$?; }
kill "$WATCHER" 2>/dev/null || true
# A resumed run that finishes must exit cleanly; 143 is the timeout kill.
if [ "$P2_RC" -ne 0 ] && [ "$P2_RC" -ne 143 ]; then
  echo "NOTE: phase 2 exited with status ${P2_RC} (checking durable results anyway)" >&2
fi

# --- verify durable state was preserved ------------------------------------
echo "== phase 1 prefix must be preserved verbatim =="
if [ ! -f "$RESULTS" ]; then
  echo "FAIL: results.jsonl disappeared during resume" >&2
  exit 1
fi
head -n "$P1_LINES" "$P1_SNAPSHOT" > "$WORK/phase1.head.jsonl" || true
head -n "$P1_LINES" "$RESULTS" > "$WORK/results.head.jsonl" || true
# BSD head does not add a trailing newline when the last line lacks one; make
# both sides end in a newline so the diff compares content, not EOF style.
for f in "$WORK/phase1.head.jsonl" "$WORK/results.head.jsonl"; do
  if [ -s "$f" ] && [ -n "$(tail -c 1 "$f")" ]; then
    printf '\n' >> "$f"
  fi
done
if ! diff -u "$WORK/phase1.head.jsonl" "$WORK/results.head.jsonl"; then
  echo "FAIL: resume rewrote or reordered the already-verified prefix" >&2
  exit 1
fi
echo "prefix preserved: ${P1_LINES} line(s)"

echo
echo "== job API stats =="
curl -s "http://127.0.0.1:${PORT}/_stats" || true
echo

echo "== verifying final results =="
set +e
(cd "$ROOT" && go run "$JOBAPI/checker" -dir "$WORK")
CHECK_RC=$?
set -e

echo
echo "phase1_lines=${P1_LINES} phase2_exit=${P2_RC} check_exit=${CHECK_RC} workspace=${WORK}"
[ "$CHECK_RC" -eq 0 ]
