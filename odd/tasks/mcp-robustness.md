# MCP robustness and server status

Objective: make the MCP client survive contact with real servers and make its state visible. Today `internal/mcp` starts every configured server in one serial pass with a single shared 10s context, aborts the whole load on the first failure, and reports nothing: a broken server silently takes the others down (which servers survive depends on Go's random map order), a hung server hangs the turn, and the user has no way to see what is connected. `internal/lsp` already had to work around the read loop and the framing (see `odd/tasks/lsp-diagnostics.md`, D3), which is the same missing-layering smell from a second angle.

Branch: `main` (staged work-unit commits, no worktree — matches `odd/tasks/provider-registry.md`; the worktree offer was declined in this session)
Delivery strategy: `ask-on-risk`
Test runner: `go test ./...`

## Specs

- **S1** `internal/mcp` gains a `Manager` that owns the session's servers and exposes their status. `InitAll` replaces `InitAndWrapAll` and never fails as a whole: it always returns a Manager, connected or not.
- **S2** Server startup is best-effort per server. A server that cannot start, complete the handshake, or list its tools is recorded as failed with its error message, and every other server still loads.
- **S3** Servers are processed in a deterministic order (sorted by name) and each gets its own init timeout, so N slow servers do not share one budget and the outcome does not depend on map iteration.
- **S4** Configuration keeps project-over-global precedence and now reports which scope a server came from. Symlinked config files stay ignored.
- **S5** Every model-facing call through a wrapped tool is bounded by `DefaultToolCallTimeout`, so a server that stops answering fails the call instead of hanging the turn.
- **S6** Wrapped tools respect the shared model-facing output cap (`Registry.SetOutputLimit` through the `outputLimiter` interface), reusing the same head/tail truncation as the built-in tools, with an explicit truncation footer so the model knows the output was cut.
- **S7** Wrapped tool names are sanitized into a valid function name and deduplicated across servers, so a server or tool name with unusual characters cannot produce an uncallable or colliding `mcp_<server>_<tool>`.
- **S8** `mcp.Client` supports stop followed by start: `Start` after `Stop` re-arms the lifecycle instead of making every later call fail with "mcp client stopped".
- **S9** At startup, `vibe` prints an `mcp` block naming each configured server with its scope, state, tool count, and the error when it failed. Nothing is printed when no server is configured.
- **S10** `/mcp` prints that same block on demand inside the session; `/mcp tools` lists the wrapped tool names each server exposes. With nothing configured it prints the `vibe mcp add` hint instead of an empty table.
- **S11** The protocol layer is tested against an in-repo stdio stub server (a Go test binary speaking newline-delimited JSON-RPC 2.0), so restart, per-call timeout, best-effort startup, sanitization and truncation are covered without needing a real MCP server installed.

## Tasks

- [x] T1 mcp: restartable client, per-call timeout, shared output cap, sanitized + deduped tool names, stdio stub-server tests — S5, S6, S7, S8, S11 — route: inline — commit: `5253dba`
- [x] T2 mcp: `Manager` with per-server status, deterministic order, per-server init timeout, best-effort startup, replaces `InitAndWrapAll`, plus the startup `mcp` block — S1, S2, S3, S4, S9 — route: inline — commit: `3e67228`
- [x] T3 cli/slash: `/mcp` and `/mcp tools`, help entry, README + CHANGELOG — S10 — route: inline — commit: `f9d00fb`

## Log

- L1 (user, verbatim): "que opinas del soporte de mcp? esta bien?"
- L2 (user, verbatim): "podrías corregirlo, mejorarlo. por ejemplo al ejecutar vibe q me informe que mcp estan conectados, o bien un comando /mcp para ver el estado de los servicios?"
- L3 (user, decision): keep working on `main` instead of a new worktree, even though the checkout already carried uncommitted LSP work.
- D1 (decision): the status surface is built on the existing `tui.Row` + `tui.WriteBlock` chrome instead of ad-hoc `fmt.Println`, so the startup block, `/mcp` and `/status` look like the same product and styling-on/off stay consistent.
- D2 (decision): the scope (`project`/`global`) is carried per server rather than flattened, because a project override silently shadowing a global definition is exactly the failure a status command exists to expose.
- D3 (decision): a failed server keeps its slot in the report with its error instead of being dropped; the count of connected servers is the signal, and hiding the failure would recreate the silent degradation S2 removes.
- D4 (decision): the per-call timeout is a constant (`DefaultToolCallTimeout`) rather than a config knob. A hung server is a bug, not a tuning preference, and the config surface for MCP is already the `mcp.json` file; a knob can be added when a real server legitimately needs more than the default.
- D5 (decision): `headTailTruncate` is exported as `tools.TruncateOutput` and reused by the MCP wrapper instead of duplicating the helper, so one truncation behavior serves every tool.
- D6 (decision): the startup report is a separate `mcp` block printed after the banner rather than rows folded into `startupBanner`. The banner is a pure function of `config.Config` with its own test suite; threading a runtime object through it would churn those tests for no user-visible gain.
- D7 (decision): `InitAndWrapAll` is deleted rather than kept as a wrapper around the Manager. It had one caller, and leaving a second entry point that silently reintroduces the abort-on-first-failure behavior would be a trap.
- D8 (decision, deviation from the task split as first written): the startup `mcp` block (S9) landed with T2 instead of T3. T2 deletes the `warning: failed to initialize MCP tools` line, which was the only failure signal in the product; a commit that removes a signal while adding nothing observable does not stand on its own as a work unit.
- D9 (observed): the interactive `/mcp` had to be verified over a real pty, not a redirected stdin. `PlainUI` does read a non-TTY reader, but the styled startup emits a cursor-position probe (`ESC[ 6n`) whose reply is read back from stdin, so a `printf … | vibe` or `< file` pipe is consumed by that probe and the REPL exits at the first prompt without running anything. A pty driver that answers the probe drives `/mcp` and `/mcp tools` normally. The pre-existing piped-stdin behavior was left alone as out of scope.
- D10 (observed): T1–T3 are roughly 1200 authored lines, over the 400-line review budget. They are three independent work units (client/tool hardening, the Manager plus the startup block, `/mcp` plus docs), so the slice is each commit and the overage is reported rather than absorbed by merging them, matching D8 in `odd/tasks/lsp-diagnostics.md`.
