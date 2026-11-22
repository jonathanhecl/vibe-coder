# Single context threshold for the wire window and compaction

Objective: stop the transcript window from dropping history that compaction would still have summarized — one threshold for both controllers — and make context pressure visible while it happens.

Branch: `main`
Delivery strategy: `ask-on-risk`
Test runner: `go test ./...`

## Specs

- **S1** The wire window is cut at `transcriptFullAt(budget)`, the same number the compaction trigger reads (the configured `COMPACT_AT_PERCENT` of the transcript budget, with the experimental session-history mode lowering it to 50%). A transcript below that cutoff reaches the model whole; `windowStartForBudget` keeps only the role of safety valve for what compaction cannot fix (a single oversized turn, a failed summary).
- **S2** `transcriptFullAt` is the single place that resolves the percentage, so the RAG-for-session-history override cannot apply to one controller and not the other.
- **S3** `/stats` reports live context pressure — `Context fill` as a percentage of the cutoff with both token counts — and `Messages dropped` with the deepest window cut in the run, because a request that loses history without a summary must be visible without reading the JSONL.
- **S4** `metrics.Summary` carries `MaxMessagesDropped` (the deepest cut, not a sum: `MessagesDropped` is a position in the transcript), and `vibe-metrics` prints it as a `DROPPED` column and a `max messages dropped` diff row, so the invariant S1 establishes can be verified in an A/B run.

## Tasks

- [x] T1 agent: add `transcriptFullAt`, use it in `compactBestEffort` and `buildOllamaMessages`, delete the duplicated package-level window helper, and expose `ContextPressure` — S1, S2
- [x] T2 observability: `MaxMessagesDropped` in the summary, context/pressure rows in `/stats`, `DROPPED` column and diff row in `vibe-metrics` — S3, S4
- [x] T3 docs and evidence: README sections that described the 60% window and the 70% trigger, CHANGELOG, this record — S1, S3

## Log

- L1 (user, verbatim): "Si, arranca el 1" — the first of the three improvements proposed after reviewing context and session handling: the two budgets that fought over the same resource.
- D1 (decision): unify upward (window follows the compaction trigger) instead of pushing compaction down to 60%. Compaction costs a model call and rewrites the transcript; dropping costs nothing and is invisible. Moving compaction earlier would pay the expensive path twice as often to protect history the window was throwing away for free.
- D2 (decision): the window stays as a safety valve rather than being removed. A single oversized turn and a failed summary still need a cut, and the last message is always kept so no request ever goes out empty.
- D3 (decision): `MaxMessagesDropped`, not a sum. The entry field records where the window started, which is a position, so adding positions across requests would produce a number with no meaning; the deepest cut is the one that says whether the invariant held.
- D4 (observed): the existing `TestWindowRecomputeStaysUnderBudget` asserted the 60% cutoff, so it was rewritten into two tests that pin the new behavior: masking keeps a tool-heavy session whole, and prose the masker cannot shrink still trims above the threshold.
- D5 (observed): the package-level `windowStartForBudget` duplicated the method and used the uncalibrated estimator. Deleted: two implementations of the same cut is how the thresholds drifted apart in the first place.
