# End-to-end suites

Manual, black-box tests that build the real `vibe` binary and exercise it as a
user would. They are kept on disk for local runs but ignored by Git (see the
`e2e/` entry in `.gitignore`).

## Suites

| Suite | Model needed | What it covers |
|-------|--------------|----------------|
| [`web-research/`](web-research/) | Yes | `WebSearch` + `WebFetch`: browse the web and write a sourced report |
| [`system-hosts/`](system-hosts/) | Yes | System task: detect and add a host alias in the OS hosts file |
| [`code-edit/`](code-edit/) | Yes | Code editing: fix failing tests with minimal, scoped edits |
| [`mission-jobapi/`](mission-jobapi/) | Yes | Autonomous mission: poll/retry a job API, verify results |
| [`mission-resume/`](mission-resume/) | Yes | Crash mid-mission (`SIGKILL`) + `--resume` durability |

The model-driven suites default to `baytout3/Gemma-4-Uncensored-HauhauCS-Aggressive:e4b`;
override with `MODEL=...`. They need a model that supports tool calling.

Every suite builds `vibe` into its own `work/` directory, so a run never
touches a previously installed binary. `work/` is disposable and is removed at
the start of each run.

## Prerequisites

- Go `1.26+` (see `go.mod`) on `PATH`.
- For `mission-*`: a running Ollama instance and a **tool-calling** model.

> The CLI flow checks (`--version`, `--help`, MCP servers, skills) are now an
> automated Go test: see `cmd/vibe/cliflow_test.go`.

## web-research

Real-internet browsing test (requires network access). The agent gets a pinned
guide (`context.md`) and must:

1. Run at least two different `WebSearch` queries.
2. `WebFetch` at least three result pages and read them.
3. Write `report.md` with a title, an intro, a `## Key facts` section, and a
   `## Sources` section listing every URL it fetched.

`checker/` validates the report: at least 150 words, at least 3 distinct URLs,
a Sources section, at least 3 topic fact markers, and at least 2 cited URLs
still returning HTTP 200 (so hallucinated links fail). The agent output is
captured in `work/run.log` and the report is printed to the console.

The content check is intentionally strict: a model that browses well but writes
a generic article without the requested origin/creator facts will fail, which
is the signal this suite is meant to surface. Read the printed report to judge
the summary quality yourself.

```bash
bash e2e/web-research/run.sh
```

Environment variables (all optional):

| Variable | Default | Meaning |
|----------|---------|---------|
| `MODEL` | `baytout3/Gemma-4-Uncensored-HauhauCS-Aggressive:e4b` | Ollama model |
| `OLLAMA_HOST` | `http://127.0.0.1:11434` | Ollama base URL |
| `TIMEOUT_SECS` | `1800` | Hard limit for the whole run |
| `MAX_TOKENS` | `8192` | `--max-tokens` passed to vibe |
| `EXTRA_FLAGS` | `--no-think` | Extra flags passed to vibe |
| `TOPIC` | Go programming language origin | Research topic |

There is also a model-free connectivity probe:

```bash
go run ./e2e/web-research/probe "Go programming language history"
```

It prints the raw `WebSearch` results and a `WebFetch` preview, which is handy
to tell a search-backend problem apart from a model problem.

## mission-jobapi

The baseline autonomous-mission test. The agent gets a pinned guide
(`mission-context.md`) and a local async job API (`server.go`):

1. Submit a job (`POST /jobs`).
2. Poll it until `done`/`failed` (`GET /jobs/<id>`); it stays `running` for a
   couple of polls on purpose.
3. Fetch the result once done (`GET /jobs/<id>/result`).
4. The API fails the first attempt of every Nth job, so the agent must retry
   with the same prompt (up to 3 attempts).
5. Record only results whose `value` matches the dataset `expect` in
   `results.jsonl`.

`checker/` then asserts every dataset item has exactly one `verified` line,
with the right value, in dataset order, and no duplicates.

```bash
bash e2e/mission-jobapi/run.sh
```

Environment variables (all optional):

| Variable | Default | Meaning |
|----------|---------|---------|
| `MODEL` | `baytout3/Gemma-4-Uncensored-HauhauCS-Aggressive:e4b` | Ollama model |
| `OLLAMA_HOST` | `http://127.0.0.1:11434` | Ollama base URL |
| `PORT` | `8799` | Job API listen port |
| `POLL_TICKS` | `2` | Polls a job stays `running` |
| `FAIL_EVERY` | `3` | Fail the first attempt of every Nth job (`0` disables) |
| `TIMEOUT_SECS` | `1800` | Hard limit for the whole agent run |
| `MAX_TOKENS` | `8192` | `--max-tokens` passed to vibe |
| `EXTRA_FLAGS` | `--no-think` | Extra flags passed to vibe |
| `DATASET` | `mission-jobapi/dataset.jsonl` | Input dataset (JSONL) |

## mission-resume

Crash-durability test. It reuses the job API and checker from
`mission-jobapi/`, so the only new behaviour under test is crash + resume.

1. **Phase 1** starts the mission and runs until `KILL_AFTER` results are on
   disk, then sends `SIGKILL` (no cleanup). The kill only happens after the
   results file has been stable for two seconds and every line parses as JSON
   (via `progresscheck/`), so the crash never lands inside a record.
2. **Phase 2** runs `vibe --resume -p "continue"` on the same working
   directory and must finish the remaining items.
3. Asserts the already-verified prefix is byte-for-byte preserved (no rewrite,
   no reorder), and the checker asserts all items end up verified exactly once.

`HOME` is isolated under `work/home`, so `--resume` can only find the session
this run persisted.

```bash
bash e2e/mission-resume/run.sh
```

Environment variables (all optional, in addition to the shared ones):

| Variable | Default | Meaning |
|----------|---------|---------|
| `MODEL` | `baytout3/Gemma-4-Uncensored-HauhauCS-Aggressive:e4b` | Ollama model |
| `OLLAMA_HOST` | `http://127.0.0.1:11434` | Ollama base URL |
| `PORT` | `8798` | Job API listen port |
| `POLL_TICKS` | `2` | Polls a job stays `running` |
| `FAIL_EVERY` | `3` | Fail the first attempt of every Nth job |
| `KILL_AFTER` | `2` | Verified lines required before the crash |
| `DATASET_SIZE` | `6` | Expected number of dataset items |
| `PHASE1_TIMEOUT` | `900` | Seconds allowed to reach `KILL_AFTER` |
| `PHASE2_TIMEOUT` | `2700` | Seconds allowed for the resumed run |
| `MAX_TOKENS` | `8192` | `--max-tokens` passed to vibe |
| `EXTRA_FLAGS` | `--no-think` | Extra flags passed to vibe |
| `DATASET` | `mission-resume/dataset.jsonl` | Input dataset (JSONL) |

### Quick smoke run

The full 6-item mission can take a long time on slow local models (turns of
several minutes are common). To validate the harness quickly, use a small
dataset and crash after the first item:

```bash
printf '%s\n' \
  '{"id":"item-01","prompt":"red fox","expect":"red fox-ok"}' \
  '{"id":"item-02","prompt":"blue mountain","expect":"blue mountain-ok"}' \
  > /tmp/ds2.jsonl

MODEL="baytout3/Gemma-4-Uncensored-HauhauCS-Aggressive:e4b" \
DATASET=/tmp/ds2.jsonl DATASET_SIZE=2 KILL_AFTER=1 \
bash e2e/mission-resume/run.sh
```

If the first model finishes the mission before the crash, the script prints a
note that the run only exercised resume idempotency.

## system-hosts

System-configuration test: the agent must inspect the OS hosts file, detect
whether a host alias exists, add it if missing, and verify the result.

**Safety:** the real `/etc/hosts` is never modified. The agent works on a copy
at `work/hosts` (the pinned guide points it there), and the script fails if the
real file's hash changes during the run.

Two phases:

1. **Missing** — `work/hosts` starts without the alias; the agent must add
   `192.168.0.33 mac-mini.local`.
2. **Present** — the agent runs again; it must detect the existing entry and
   not create a duplicate (set `RUN_IDEMPOTENCY=0` to skip).

`checker/` verifies the alias maps to the right address exactly once and that
the pre-existing entries are preserved.

```bash
bash e2e/system-hosts/run.sh
```

Environment variables (all optional):

| Variable | Default | Meaning |
|----------|---------|---------|
| `MODEL` | `baytout3/Gemma-4-Uncensored-HauhauCS-Aggressive:e4b` | Ollama model |
| `OLLAMA_HOST` | `http://127.0.0.1:11434` | Ollama base URL |
| `IP` | `192.168.0.33` | Address the alias must map to |
| `NAME` | `mac-mini.local` | Alias to configure |
| `RUN_IDEMPOTENCY` | `1` | Run the second (already-configured) phase |
| `TIMEOUT_SECS` | `1200` | Hard limit per agent run |
| `MAX_TOKENS` | `8192` | `--max-tokens` passed to vibe |
| `EXTRA_FLAGS` | `--no-think` | Extra flags passed to vibe |
| `REAL_HOSTS` | `/etc/hosts` | File whose hash must stay unchanged |

## code-edit

Code-editing test. A small Go project (`fixture/`) has two bugs and a failing
test suite; the agent must run the tests, locate the bugs in the non-test
source files, fix them with minimal edits, and leave everything else alone.

It exercises `Bash` (`go test`), `Read`, and `Edit`, plus the file checkpoints
(the sandbox is a Git repo with a baseline commit).

`checker/` requires that `go test ./...` passes and that `git status` shows
changes ONLY in `calc/calc.go` and `text/text.go` — no test, `go.mod`,
`NOTES.md`, or new-file changes. The agent's diff is printed to the console.

```bash
bash e2e/code-edit/run.sh
```

Environment variables (all optional):

| Variable | Default | Meaning |
|----------|---------|---------|
| `MODEL` | `baytout3/Gemma-4-Uncensored-HauhauCS-Aggressive:e4b` | Ollama model |
| `OLLAMA_HOST` | `http://127.0.0.1:11434` | Ollama base URL |
| `TIMEOUT_SECS` | `1800` | Hard limit for the agent run |
| `MAX_TOKENS` | `8192` | `--max-tokens` passed to vibe |
| `EXTRA_FLAGS` | `--no-think` | Extra flags passed to vibe |

## Automated coverage (go test)

Some scenarios are hermetic and run as ordinary Go tests, so they need no
external model, no network, and no manual step:

- **Anti-loop** — `cmd/vibe/antiloop_test.go` starts an in-process fake Ollama
  (`httptest`) and drives the real client/agent/mission loop. It asserts that a
  mission which repeats the same no-op tool call (or returns empty responses)
  **pauses itself** with a bounded number of chat calls instead of spinning.
  The guard itself is unit-tested in `internal/agent/loopguard_test.go`.
- **CLI flows** — `cmd/vibe/cliflow_test.go` builds the binary once and runs
  `--version`, `--help`, and MCP/skill add/list/remove against an isolated
  `HOME`, asserting secret masking and `0600` permissions. The underlying
  behavior is also unit-tested in `internal/mcp` and `internal/skills`.

Run them with the rest of the suite:

```bash
go test ./...
```

The suites below remain manual because they need a real Ollama model, network
access, or long-running autonomous behavior.

## Reading the output

Each `run.sh` prints its own `PASS`/`FAIL` lines and exits non-zero on failure.
Most artifacts land under the suite's disposable `work/`: the generated
`results.jsonl` (the durable artifact both mission suites verify), the job API
log at `work/server.log`, and `work/phase1.log` / `work/phase2.log` for
mission-resume. In `mission-jobapi` the agent output is streamed to the
console; in `mission-resume` it is captured in the phase logs.

Because `EXTRA_FLAGS` defaults to `--no-think`, pass
`EXTRA_FLAGS="--think low"` (or another level) to exercise thinking models.
