# Context, Memory, and Token Efficiency for Local Models

Objective: reduce per-turn token waste, preserve Ollama's prefix KV-cache across user turns, prevent synthetic context blocks from polluting the transcript and vector memory, deduplicate superseded file reads in `Read -> Edit -> Read` loops, filter tool schemas by active mode, and make tiered session memory cleaner and durable across resumes.

Branch: `main` (staged work-unit commits, no worktree — matches `odd/tasks/lsp-diagnostics.md`)
Delivery strategy: `ask-on-risk`
Test runner: `go test ./...`

## Specs

- **S1** `recordChatAttempt` (`internal/agent/metrics.go`) includes `ToolSchemaTokensEst` in the estimated prompt token count passed to `Session.ObservePromptUsage` whenever native tool schemas are sent on the wire (`!a.xmlToolMode()`). This stops the ~2.5k–3.5k tool-schema token payload from inflating `TokenCalibrator`'s correction factor toward `2.0x` on short prompts.
- **S2** `Session` exposes its calibrated token adjustment (`CalibrateTokens(raw int) int`) and the agent's budget/windowing calculations (`computeBudget`, `compactBestEffort`, `windowStartForBudget`) price transcript and system tokens through it so window trimming and compaction triggers follow the provider's real tokenization ratio.
- **S3** `buildSystemPrompt` (`internal/agent/chat.go`) keeps the `system` message static across normal turns by removing the per-turn `goal` suffix from the system prompt. Changing message `0` on every user prompt previously invalidated 100% of Ollama's prefix KV-cache for the entire conversation history.
- **S4** The active user goal is anchored via `runtimeStateNote` (`internal/agent/progress.go`) at the ephemeral tail of the wire window when the turn is in a multi-step loop (`a.ToolCallsThisTurn() > 0`, `a.hasPendingTodos()`, `a.MissionActive()`, or after a compaction in the session), avoiding duplicate prompt text on single-shot turns while keeping the goal adjacent to the generation frontier during multi-step work.
- **S5** Additive per-run context blocks (`[RAG Context]` and `[Session memory]` in `internal/agent/context.go`) are attached as ephemeral turn context on the wire rather than appended permanently to `Session.messages` on every `Run`. Only real user prompts, assistant turns, tool observations, and runtime notes persist in the transcript.
- **S6** `demoteTexts` (`internal/session/memory.go`) and `firstUserMessageIndexLocked` (`internal/session/session_core.go`) ignore synthetic envelopes (`[Session memory]`, `[RAG Context]`, `[Earlier conversation summary]`, `[runtime_reminder]`, `[runtime]`), preventing compaction from embedding memories-of-memories or mistaking synthetic blocks for the original user request.
- **S7** `maskToolResults` (`internal/agent/mask.go`) includes the target identifier (`file_path`, `pattern`, `command`, or `query`) from the preceding assistant tool call inside the `[elided]` stub (for example, `[elided] Read (internal/agent/chat.go) output (4120 chars) from an earlier step. First line: ...`), so the model retains awareness of which files and commands it already inspected after masking.
- **S8** Even within the unmasked recent $K$-turn window, `maskToolResults` immediately elides earlier `Read` results for a `file_path` when a later successful `Read` (covering the same or full range) or a later successful `Write`/`Edit` on that same `file_path` appears in the transcript, eliminating duplicate multi-KB file dumps during `Read -> Edit -> Read` verification cycles.
- **S9** `tools.EffectOf` (`internal/tools/tool.go`) classifies `ReadManyTool`, `BackgroundStatusTool`, and `BackgroundListTool` as `EffectRead`, and `BackgroundRunTool` / `BackgroundKillTool` as `EffectExecute`. Both `nativeChatTools` and `RenderXMLProtocolBlock` filter out tools that are unconditionally blocked in the active mode (`reviewMode`, `planMode`, `offline`), cutting fixed schema token overhead and preventing small models from calling forbidden tools.
- **S10** `sessionMemory` (`internal/session/memory.go`) pairs assistant tool-call intents with their tool outputs in `demoteTexts`, deduplicates demoted fragments by content hash, enforces a minimum cosine similarity threshold (`minMemoryScore = 0.25`) in `recall` to suppress unrelated noise, and persists demoted fragment texts in a lightweight session sidecar so `HydrateMemory` can restore compacted history after `--resume`.

## Tasks

- [x] T1 (`c23aaa5`) agent, session: include `ToolSchemaTokensEst` in `ObservePromptUsage` + apply calibrated token estimates to budget and windowing + tests — S1, S2 — route: inline
- [x] T2 (`9494020`) agent, prompt: keep `buildSystemPrompt` KV-cache stable across user turns + move goal anchor to ephemeral `runtimeStateNote` on multi-step/compacted turns + tests — S3, S4 — route: inline
- [x] T3 (`fd807f9`) agent, session: move `[RAG Context]` and `[Session memory]` to ephemeral wire injection + filter synthetic envelopes from `demoteTexts` and `firstUserMessageIndexLocked` + tests — S5, S6 — route: inline
- [x] T4 (`0def3b6`) agent: argument-aware `[elided]` stubs + immediate wire elision of superseded `Read` outputs on re-read or `Write`/`Edit` + tests — S7, S8 — route: inline
- [x] T5 (`7757d56`) tools, agent: complete `EffectOf` for `ReadMany` and `Background*` + filter tool schemas/catalogue by `reviewMode`, `planMode`, and `offline` + tests — S9 — route: inline
- [x] T6 (`37e99a5`) session: pair tool intent with output in `demoteTexts`, deduplicate entries, add cosine similarity floor in `recall`, and persist demoted memory texts across session save/load + tests — S10 — route: inline

## Log

- L1 (user, verbatim): "que te parece mi harness para modelos locales? puedes hacer mas eficiente el uso de tokens, y se organice mejor la memoria y el contexto para que el agente sea mas capaz :)"
- L2 (user, verbatim): "claro, excelente! armamos un plan que ataque los 5 puntos"
- L3 (user, verbatim): "Muy bien, comenzemos. Haz commit cada step :)"
- D1 (decision): split the 5 architectural improvements into 6 focused, independently testable work-unit commits (T1–T6) ordered from foundational token measurement and KV-cache stability (T1–T2) to context/wire hygiene (T3–T5) and durable tiered memory (T6).
- D2 (decision): keep `a.reg` unfiltered for execution-time policy checks (`toolModeBlock`) while filtering `nativeChatTools()` and `RenderXMLProtocolBlockForMode()` so forbidden tool schemas are omitted from the model prompt without weakening runtime enforcement.

