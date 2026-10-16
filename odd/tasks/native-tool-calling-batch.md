# Native Tool-Calling Batch Ordering and Session Isolation

Objective: ensure strict compliance with native tool-calling protocols (OpenAI and Ollama) on multi-tool batches, preventing HTTP 400 errors and wire transcript corruption when tool #1 performs file mutations or when tools are blocked/denied, and isolate SubAgent executions from polluting the parent session transcript.

Branch: `main`
Delivery strategy: `ask-on-risk`
Test runner: `go test -v ./internal/agent -run "TestNative.*"`

## Specs

- **S1** Auxiliary post-edit observations (`fileEditCompletionNote`, `observeEditDiagnostics`, and `autoTest.RunAfterEdit`) are deferred during a native tool-calling batch and appended only after all `role: "tool"` results for the batch have been recorded in `a.sess`.
- **S2** When a tool call is blocked by mode (`toolModeBlock`) or denied by permissions (`perm.Check`) in `mode.structured`, `executeTool` records the denial/block as a `role: "tool"` result (`a.sess.AddToolResult(toolName, note)`), avoiding missing tool responses.
- **S3** If execution halts early during a native batch (e.g. tool denied or failed), all remaining declared tool calls in `nativeCalls` receive an answered `role: "tool"` result (`a.sess.AddToolResult(remCall.Name, "Tool execution skipped: previous tool call in batch was denied or failed.")`), ensuring the declared `assistant.tool_calls` are never left orphaned on the wire.
- **S4** `executeDelegatedTool` runs with `toolExecutionMode.skipSessionRecord = true` so child sub-agents do not pollute the parent session transcript with intermediate observations.
- **S5** Unknown tools (`a.reg.Get(c.Name) == nil`) in a native batch are recorded as `role: "tool"` error results rather than panicking or leaving unanswered calls.

## Tasks

- [x] T1 agent: implement deferred auxiliary notes in `toolExecutionMode` + queue post-edit diagnostics/notes/auto-tests until all native batch results are recorded — S1
- [x] T2 agent: answer blocked, denied, unknown, and skipped native tool calls with structured `role: "tool"` results so `assistant.tool_calls` are never orphaned — S2, S3, S5
- [x] T3 agent: set `skipSessionRecord: true` in `executeDelegatedTool` to isolate child subagent transcripts from parent session — S4
- [x] T4 tests: add comprehensive regression tests in `native_history_test.go` and `delegation_test.go` covering Write+Read batches, denied tools in batch, and subagent session isolation
