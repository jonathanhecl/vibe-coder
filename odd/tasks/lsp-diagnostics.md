# LSP diagnostics for edits

Objective: give the agent authoritative, cheap language-level feedback on the file it just edited. Today the only post-edit signal is `AutoTest` (`internal/git/autotest.go`), which shells out to `go test ./...`, `pytest`, `vitest` or `cargo test` and costs a minute plus a turn to discover a typo. A language server reports the same class of error in hundreds of milliseconds for tens of tokens, which is the difference between a local model converging in 3 turns and in 12.

Branch: `main` (staged work-unit commits, no worktree — matches `odd/tasks/provider-registry.md`)
Delivery strategy: `ask-on-risk`
Test runner: `go test ./...`

## Specs

- **S1** A new `internal/lsp` package holds a minimal LSP client over stdio: JSON-RPC 2.0 framing with `Content-Length` headers, `initialize` + `initialized`, `textDocument/didOpen`, `textDocument/didChange` (full-text sync) and the `textDocument/publishDiagnostics` notification. Only this subset of LSP 3.17 is implemented, so gopls-style push diagnostics are enough and no pull request (`textDocument/diagnostic`) is needed.
- **S2** Servers are resolved per language from the same workspace markers the auto-test already probes (`internal/git/autotest.go:detect`): `go.mod` → `gopls`, `package.json` → `typescript-language-server`, `pyproject.toml`/`pytest.ini` → `ruff server`/`pylsp`, `Cargo.toml` → `rust-analyzer`. A server is usable only when its binary is on `PATH` (`exec.LookPath`); the first positive resolution is cached for the session, mirroring `AutoTest.detectCached`.
- **S3** One server process per language per session, started lazily on first need and reused across every edit and turn. `shutdown` + `exit` on agent exit, process killed on cancellation, so no orphan language server is ever left behind (the same cleanliness bar as isolated sessions).
- **S4** Diagnostics are push-based and therefore asynchronous. After `didChange`, the client waits for the notification carrying the version it just sent, bounded by a timeout (default 5000 ms). On timeout it returns what arrived, or nothing; it never hangs the turn.
- **S5** The model-facing payload is severity-first and bounded: `error`, then `warning`, then `info`/`hint`; one line per diagnostic as `<file>:<line>:<col> <severity> <message> [<source>]`; the whole result passes through the shared output cap (`tools.OutputLimitBytes`) and truncation is stated explicitly with the count left out, like the `Read` truncation note.
- **S6** A `Diagnostics` tool is exposed to the model, accepting `file_path` and an optional `severity` filter. It is read-only (`EffectRead` in `tools.EffectOf`), so it stays available under plan and review mode and never asks for permission. On a clean file it answers in one line instead of nothing, so the model knows the check actually ran.
- **S7** Automatic post-edit path: after a successful `Write`/`Edit`, diagnostics for the edited file are recorded as a `[DIAGNOSTICS]` observation next to the existing `[AUTO-TEST]` call in `internal/agent/tool_exec.go`, through the same `recordToolObservation` route. It runs only when the LSP is enabled.
- **S8** On a single edit, diagnostics run before the auto-test, and when they report at least one error the auto-test is skipped for that edit: a red squiggle that arrived in 200 ms tells the model more than a suite run that costs a minute, and the model can re-run tests itself once the file is clean.
- **S9** Configuration follows `defaults < config file < env < CLI`: `LSP_ENABLED` (auto — on when a server is available), `LSP_SERVERS` (override the language → command map), `LSP_AFTER_EDIT` (on), `LSP_TIMEOUT_MS` (5000), with `VIBE_CODER_LSP_*` env variants. `/status` reports the resolved state.
- **S10** A broken language server never degrades the session: a missing binary, a failed handshake, a crashing server or an unsupported language all resolve to "no diagnostics", silently as far as the model is concerned and only visible in the debug log.
- **S11** Diagnostics are injected as transcript observations only. They must not touch the system prompt, which was reordered for KV-cache friendliness.
- **S12** The prompt template tells the model that diagnostics are authoritative for the file it edited and that it should clear reported errors before running the test suite.
- **S13** The protocol layer is tested against a stub language server (a Go test binary speaking the handshake over stdio), so framing, severity ordering, timeouts, truncation, and the "no server available" path are covered without requiring gopls on the test machine.

## Tasks

- [x] T1 lsp: JSON-RPC framing + client core (`initialize`, `didOpen`, `didChange`, `publishDiagnostics`) + stub-server tests — S1, S4, S13 — route: inline — commit: `eca2851`
- [ ] T2 lsp: server registry + marker/PATH detection + cached detection + process lifecycle and shutdown + tests — S2, S3, S10 — route: inline — commit: pending
- [ ] T3 tools: `Diagnostics` tool (read-only effect, shared output cap, severity-first rendering, truncation footer) + registration + tests — S5, S6 — route: inline — commit: pending
- [ ] T4 agent: `[DIAGNOSTICS]` post-edit observation, diagnostics-before-autotest ordering, auto-test skipped on reported errors + tests — S7, S8, S11 — route: inline — commit: pending
- [ ] T5 config: `LSP_*` file/env/flag precedence + `/status` reporting + tests — S9 — route: inline — commit: pending
- [ ] T6 docs: README (Features, Built-in Tool Notes, `/status`), CHANGELOG, prompt template guidance — S12 — route: inline — commit: pending

## Log

- L1 (user, verbatim): "vamos con el LSP"
- D1 (decision): the LSP lands as one engine with two entry points — an on-demand `Diagnostics` tool and an automatic post-edit observation — because the automatic path is where the leverage is for a local model (it does not know it should check) while the tool is the smaller, safer first step and is reused by the automatic path.
- D2 (decision): diagnostics replace the auto-test on a red edit rather than running beside it. Running both would pay the expensive signal for information the cheap signal already delivered.
- D3 (decision): the `Content-Length` framing is implemented in `internal/lsp` instead of being reused from `internal/mcp`. That client exchanges newline-delimited JSON and drops every message without an id, which is exactly the shape of a `publishDiagnostics` notification, so neither its framing nor its read loop matched what push diagnostics need.
- D4 (decision): the client answers every server request, unsupported ones with method-not-found. gopls asks `workspace/configuration` during startup and publishes nothing until that request is settled, so an unanswered request is a silent hang rather than a missing feature.
- D5 (decision): S4's "returns what arrived, or nothing" is made explicit as a `bool` on the wait, and a clean file is a successful wait with an empty list rather than a timeout. A caller can then never present a list published for an older version as the answer to the edit it just made.
- D6 (decision): `textDocument/didClose` is added to the T1 surface beyond S1's list. A server reused across turns (S3) has to be able to release a document, and the call is part of the same protocol layer rather than of T2's lifecycle.
- D7 (decision, deviation from S13 as worded): the stub language server runs in-process over `net.Pipe`, not as a separate test binary on stdio. The client takes an `io.ReadWriteCloser`, so the process boundary is T2's business and framing, version-matched waits, timeouts and the "no publication" path are covered without a binary. The stub writes its own `Content-Length` headers by hand so a bug in `WriteMessage` cannot hide behind the stub built on it.
- D8 (observed): T1 landed as one work-unit commit of 1950 authored lines, over the 400-line review budget. It is a single unit — framing, the client core and the typed protocol layer have no consumer apart from each other — so the slice is the review candidate and the overage is reported rather than absorbed by splitting a package that does not compile usefully in halves.
