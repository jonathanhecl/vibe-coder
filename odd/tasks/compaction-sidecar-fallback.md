# Compaction falls back to the main model

Objective: a sidecar failure during session compaction must not leave the context empty or half-summarized — the main model retries the same summary before the transcript degrades to the static truncation note.

Branch: `main` (work-unit commit, follows `odd/tasks/context-window-threshold.md`)
Delivery strategy: `ask-on-risk`
Test runner: `go test ./...`

## Specs

- **S1** Compaction resolves its summarizers as an ordered chain (`compactionTargets`): the sidecar when it is in use, bound to the dedicated sidecar client or, when none is set, to the main client serving the sidecar model name; then the main model with its own `keep_alive`.
- **S2** An attempt that returns an error or a blank reply is retried on the next target (`requestCompactionSummary` returns an empty string, the chain treats it as "not summarized"). A target whose model name repeats the previous one is dropped, because retrying the same model asks the same question twice.
- **S3** The chain stops as soon as one target produces a summary, and stops early when the parent context is already cancelled or expired so a deadline is not spent twice. When every target fails, compaction still succeeds with the existing `Earlier conversation truncated to stay within context limits.` note.
- **S4** Unchanged behaviour elsewhere: a working sidecar still makes exactly one request, a setup without a sidecar still summarizes on the main model, and `/compact` keeps working through the same path.

## Tasks

- [x] T1 session: extract `compactionTargets` and `requestCompactionSummary`, replace the single sidecar attempt with the retry chain — S1–S3
- [x] T2 tests: fallback on error, fallback on a blank reply, static note when every model fails, plus a table test for the chain order and dedup — S2–S3
- [x] T3 docs: README compaction bullet and sidecar section (which also corrects the stale claim that an unset sidecar leaves the static note), CHANGELOG, this record — S4

## Log

- L1 (user, verbatim): "si el sidecar falla en el compact de la session, recaer en el main, para que no quede vacio o fallido el contexto."
- D1 (decision): an ordered chain, not a swap. The cheap sidecar stays the preferred summarizer; the main model is the safety net, so the common case keeps its cost and only the failure case pays for the bigger model.
- D2 (decision): a blank reply counts as a failure. Ollama answers 200 with empty content when the model was evicted or the request was rejected in a way it reports softly, so storing that as the summary is exactly how the context ends up empty; the check is `strings.TrimSpace(resp.Content) == ""`.
- D3 (decision): dedup by model name only. Clients are interface values that may be non-comparable structs (the test fakes are), and comparing them would panic; two targets sharing a model name would ask the same model the same question anyway.
- D4 (decision): the chain checks `ctx.Err()` between attempts. Without it, a 90-second sidecar timeout would be followed by another 90 seconds on main while the run that requested the compaction may already be gone.
- D5 (correction): the README paragraph that told users an empty `SIDECAR_MODEL` means compaction "will truncate to a static note" was already false — `TestCompactFallsBackToMainModelWhenSidecarUnset` has always shown the main model running it. Fixed here because the fallback work is what makes the paragraph's subject accurate.
