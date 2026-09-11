#!/usr/bin/env bash
# End-to-end system-configuration test: the agent must inspect the OS hosts
# file, detect that a host alias is missing, add it, and verify it.
#
# Safety: the real /etc/hosts is NEVER modified. The agent works on a copy in
# the sandbox (work/hosts), and the script fails if the real file changes.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$DIR/../.." && pwd)"

MODEL="${MODEL:-baytout3/Gemma-4-Uncensored-HauhauCS-Aggressive:e4b}"
OLLAMA_HOST="${OLLAMA_HOST:-http://127.0.0.1:11434}"
TIMEOUT_SECS="${TIMEOUT_SECS:-1200}"
MAX_TOKENS="${MAX_TOKENS:-8192}"
EXTRA_FLAGS="${EXTRA_FLAGS:---no-think}"
RUN_IDEMPOTENCY="${RUN_IDEMPOTENCY:-1}"

IP="${IP:-192.168.0.33}"
NAME="${NAME:-mac-mini.local}"

WORK="$DIR/work"
rm -rf "$WORK"
mkdir -p "$WORK/home"
cp "$DIR/hosts.fixture" "$WORK/hosts"
sed "s#__HOSTS_FILE__#${WORK}/hosts#g" "$DIR/context.md" > "$WORK/context.md"

echo "== building vibe =="
(cd "$ROOT" && go build -o "$WORK/vibe" ./cmd/vibe)

hash_file() { shasum -a 256 "$1" 2>/dev/null | awk '{print $1}'; }
REAL_HOSTS="${REAL_HOSTS:-/etc/hosts}"
REAL_BEFORE=""
if [ -f "$REAL_HOSTS" ]; then
  REAL_BEFORE=$(hash_file "$REAL_HOSTS")
fi

PROMPT="I have a PC on the network at ${IP} and I want to reach it from this machine as ${NAME}. Check whether the alias is configured in the system hosts file and, if it is not, configure it in the OS."

run_agent() {
  local log="$1"
  (
    cd "$WORK"
    HOME="$WORK/home" ./vibe \
      --model "$MODEL" \
      --ollama-host "$OLLAMA_HOST" \
      --max-tokens "$MAX_TOKENS" \
      -y $EXTRA_FLAGS \
      --context "$WORK/context.md" \
      -p "$PROMPT"
  ) >"$log" 2>&1 &
  local pid=$!
  ( sleep "$TIMEOUT_SECS"; kill -9 "$pid" 2>/dev/null ) &
  local watcher=$!
  local rc=0
  wait "$pid" || rc=$?
  kill "$watcher" 2>/dev/null || true
  return $rc
}

echo "== phase 1: alias missing, agent must configure it (model=${MODEL}) =="
run_agent "$WORK/phase1.log" && AGENT_RC=0 || AGENT_RC=$?

echo
echo "== hosts file after phase 1 =="
cat "$WORK/hosts" 2>/dev/null || true
echo "==============================="
echo

set +e
(cd "$ROOT" && go run ./e2e/system-hosts/checker -hosts "$WORK/hosts" -ip "$IP" -name "$NAME")
CHECK1=$?
set -e

CHECK2=0
if [ "$RUN_IDEMPOTENCY" = "1" ]; then
  echo
  echo "== phase 2: alias present, agent must not duplicate it =="
  run_agent "$WORK/phase2.log" && AGENT2_RC=0 || AGENT2_RC=$?
  set +e
  (cd "$ROOT" && go run ./e2e/system-hosts/checker -hosts "$WORK/hosts" -ip "$IP" -name "$NAME")
  CHECK2=$?
  set -e
fi

echo
echo "== real hosts file must be untouched =="
REAL_OK=1
if [ -n "$REAL_BEFORE" ]; then
  REAL_AFTER=$(hash_file "$REAL_HOSTS")
  if [ "$REAL_BEFORE" != "$REAL_AFTER" ]; then
    echo "FAIL: ${REAL_HOSTS} changed during the test" >&2
    REAL_OK=0
  else
    echo "real ${REAL_HOSTS} unchanged"
  fi
fi

echo
echo "agent_exit=${AGENT_RC} check1=${CHECK1} check2=${CHECK2} real_ok=${REAL_OK} workspace=${WORK}"
[ "$CHECK1" -eq 0 ] && [ "$CHECK2" -eq 0 ] && [ "$REAL_OK" -eq 1 ]
