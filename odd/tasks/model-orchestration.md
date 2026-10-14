# Model orchestration & memory

Objective: make the harness use its models optimally — per-role keep_alive, an embedding consumer for hybrid RAG, tiered memory that demotes instead of discarding, and the decision model reused as a fast judge.
Branch: `main` (user explicitly asked for staged commits on main, no worktree)
Delivery strategy: `ask-on-risk`; work-unit commits directly on `main`
Test runner: `go test ./...`

## Specs

### T1 — per-role keep_alive (speed quick win + prerequisite)

- **S1** Each role has its own `keep_alive`. The `main` model stays hot; worker roles (sidecar, decision, embedding, vision) unload after a short idle window. Defaults: main `-1` (pinned forever), worker `300` (5 minutes). (plan decision: "per-role keep_alive (main hot, workers short/lazy)")
- **S2** `keep_alive` is configurable through the normal precedence chain: config file (`KEEP_ALIVE_MAIN`, `KEEP_ALIVE_WORKER`), env (`VIBE_CODER_KEEP_ALIVE_MAIN`, `VIBE_CODER_KEEP_ALIVE_WORKER`), CLI (`--keep-alive-main`, `--keep-alive-worker`); defaults < file < env < CLI.
- **S3** The value means: `-1` pins the model forever; `0` omits the field so the provider applies its own default (Ollama: 5 minutes); `N > 0` unloads the model after `N` idle seconds. The old unconditional `0 → -1` rewrite is removed.
- **S4** Every chat call site sends the keep_alive of the role it belongs to: main chat, subagent, `/commit`, main-image description → main; sidecar chat and sidecar description → sidecar; session compaction → sidecar when the sidecar is in use, otherwise main; vision role description → vision.
- **S5** The new settings persist with `--save` and appear in the CLI help text.

### T2 — embedding consumer + hybrid RAG (pending)

- **S6** A batched embeddings client (`/api/embed`).
- **S7** Hybrid retrieval: lexical + vector fused (RRF), not vector-only.
- **S8** Symbol-aware chunking (`func`/`type` boundaries) instead of fixed 800/200 windows.
- **S9** Incremental indexing by content hash (no full rebuild each start).
- **S10** The legacy flat `RAGModel` is folded into the `embedding` role.
- **S11** When no `embedding` role is bound, RAG falls back to text-only retrieval and never fails.

### T3 — tiered memory (pending)

- **S12** Compaction demotes the discarded history into an embedding store instead of dropping it.
- **S13** Retrieval of demoted history is by current goal, gated (only under window pressure or a weak lexical match) and batched — never mandatory per turn.
- **S14** Tiers: T0 working context (masking), T1 compaction, T2 session vector store, T3 episodic memory.

### T4 — decision model as judge (pending)

- **S15** The decision model judges relevance: rerank via the `score` primitive, gate and semantic dedupe via `choice`, batched in one request.
- **S16** Judgment fallback chain: decision → sidecar → main.
- **S17** Query rewrite runs on the sidecar and is gated like retrieval.
- **S18** Relevance quality is measured empirically, not assumed.

### Review-1 leftovers folded in (pending)

- **S19** Cap `recentProgressSummary` by a token/last-N budget.
- **S20** Memoize decision verdicts for assisted mode (tool + normalized args).
- **S21** Calibrate the token estimator against real usage.

## Tasks

- [x] T1 config+ollama+call sites: per-role keep_alive + precedence + persist + tests — S1–S5 — route: inline — commit: a0d7f69
- [x] T2 rag: embeddings client + hybrid retrieval + symbol chunking + incremental hashing + fold RAGModel — S6–S11 — route: inline — commits: 23fc9d9, 247a017, b5bd1d7
- [x] T3 memory: compaction demotes to vector store + goal-based gated retrieval — S12–S14 — route: inline — commit: 2ed0f6d
- [ ] T4 judge: decision relevance/dedupe + fallback chain + query rewrite + leftovers — S15–S21 — route: inline — done: S15 (c3cc6fa), S16+S18 (0d1dfec), S19+S20 (cc9471d); S17, S21 pending

## Log

- L1 (user, verbatim): "ahora podemos ver como hacer un uso mas optimo de los modelos, esta bien tener un main y un sidecar? podemos usar embedding en el rag? el rag puede ser inteligente? puede ser temporal y usarse como contexto largo para cuando tiene que hacer tareas largas? que opinas?"
- L2 (user, verbatim): "podriamos tener un sidecar especifico dedicado al manejo de la memoria, del rag, etc? que opinas? imaginando que podriamos usar modelos eficientes enfocados en estas funciones"
- L3 (user, verbatim): "tambien tenemos modelo de decision, que podría tomar tareas de juez ya que es muy veloz. hay modelos superveloces pero no son tan inteligentes en ciertas tarea a menos que se reentrenen para tareas concretas. igualmente es una idea valida"
- L4 (user, verbatim): "como queda la idea finalmente, que vamos a hacer?"
- L5 (user, verbatim, authorization): "esta perfecto. si, inicialo, haz commits de forma regular a medida q avancemos, todo branch main. como fallback si no hay modelo decision, usar sidecar, sino hay sidecar cae en main. si no hay embedding será rag en texto."
- L6 (decisions): role count stays at five; the only new leg is the embedding encoder consumer. Judgment reuses the decision model; encoder reuses the embedding role. Deterministic work (chunking, gating, hashing, path truncation) is moved off the LLM. Sequence: T1 → T2 → T3 → T4.
- L7 (progress, T1, commit a0d7f69): per-role keep_alive shipped. `internal/config`: `KeepAliveMain`/`KeepAliveWorker` + defaults -1/300 + `KeepAliveFor(role)` + `KEEP_ALIVE_MAIN`/`KEEP_ALIVE_WORKER` file keys, `VIBE_CODER_KEEP_ALIVE_*` env and `--keep-alive-main/worker` flags + persistence + help text. `internal/ollama`: `keep_alive,omitempty` and the unconditional 0→-1 rewrite removed, so 0 now means "provider default". Call sites send their role value: main chat, subagent, `/commit` and main image description → main; sidecar chat/description → sidecar; compaction → sidecar-or-main as actually used; vision role → vision. Evidence: `gofmt -l` clean on touched files; `go vet ./...` silent; `go build ./...` exit 0; `go test ./...` fully green (config 0.301s, ollama 0.387s, sidecar 0.320s, visionrole 0.242s, tools 2.011s, session 0.356s, agent 18.597s, slash 0.535s, cmd/vibe 24.064s). New tests: `internal/ollama/keepalive_test.go` (marshal 0 omits / -1 and N sent; Chat preserves the value) and `internal/config/keepalive_test.go` (defaults, precedence file<env<CLI, nil-safe accessor, `--save` persistence). README and CHANGELOG updated.
- L8 (progress, T2, commits 23fc9d9 + 247a017 + b5bd1d7): hybrid RAG shipped. T2a `internal/ollama/embed.go` adds a batched `/api/embed` client behind a narrow exported `Embedder` interface (kept out of the `Client` interface so the OpenAI adapter and six test stubs stay untouched); `*HTTPClient` satisfies it, `*openai.Client` does not → text-only fallback. T2b rewrote `internal/rag/engine.go`: a `files(path,hash)` table plus an `embedding` column (auto-`ALTER`-migrated) make indexing incremental by content hash and prune vanished files; chunks are split at declaration boundaries (`splitCodeChunks` in `text.go`) and embedded in batches of 32; retrieval ranks lexically and by vector cosine then fuses with RRF (k=60), and any embedder error degrades to lexical-only without failing the index. T2c removed the legacy flat `RAGModel`/`--rag-model` and wired the encoder from the `embedding` role via `embeddingClientFor` (mirrors `visionClientFor`, returns the main client when unbound). Evidence: `gofmt -l` clean on touched files (three pre-existing unformatted files left untouched); `go build`/`go vet` clean with and without `-tags rag`; `go test ./...` fully green and `go test -tags rag ./...` fully green (rag 0.401s). New tests: `internal/ollama/embed_test.go` (batch POST, auth header, model/inputs, mismatched vectors, HTTP error) and `internal/rag/index_test.go` (symbol chunking, incremental + prune, embedder-failure degradation, hybrid vector hit via RRF, legacy-schema migration). README and CHANGELOG updated.
- L9 (progress, T3, commit 2ed0f6d): tiered memory shipped. `internal/session/memory.go` adds a session-scoped, in-memory embedding store of demoted history (batched `Embed`, cosine recall, 256-entry cap, disabled when no embedding role is bound). `CompactTo` demotes the discarded slice before replacing the transcript, outside the lock, and never fails compaction. The agent recalls the most relevant fragments only under pressure (`recallMemoryWanted`: right after a compaction, or past half the context window) and batched, so it is never a per-turn cost. Evidence: `gofmt -l` clean; `go build`/`go vet` clean with and without `-tags rag`; `go test ./...` fully green (session 0.339s, agent 18.516s); new `internal/session/memory_test.go` covers demote/recall, the disabled paths, the entry cap and the compaction integration. Backward compatibility was explicitly not required (this version starts from scratch), so no migration was added.
- L10 (progress, T4a, commit cc9471d): the two deterministic quick wins landed. S19: `recentProgressSummary` in `internal/agent/mission.go` now keeps only the newest system/tool messages that fit a 1500-token budget (`progressSummaryTokenBudget`), clipping a single oversized message via `clipProgressText` instead of dropping it, so the judge prompt cannot grow without bound. S20: `internal/permissions/manager.go` memoizes successful assisted-mode decision verdicts in a bounded 128-entry FIFO keyed by tool + normalized detail + context note; only successful verdicts are stored, so a failure or an unavailable decider still falls back to manual prompts, and `SetSafetyDecider` resets the memo. Tests: `internal/agent/mission_progress_budget_test.go` and `internal/permissions/verdict_cache_test.go`. Evidence: gofmt clean on the touched files; `go build ./...` exit 0; `go vet ./...` clean; `go test ./...` fully green (agent 18.5s, permissions 0.30s). Remaining T4: S15–S18 (decision as judge) and S21 (estimator calibration).
- L11 (progress, T4b, commits c3cc6fa + 58573af + 0d1dfec): the decision model now judges retrieval. S15 (`c3cc6fa`) adds `ScoreRelevance(ctx, query, candidates)` to `internal/decision/decision.go`: one batched `score` request for every candidate (`relevance0`, `relevance1`, ...) with a sequential `callScore` fallback, normalized to [0,1], and a clear error when the endpoint is unconfigured. S16+S18 (`0d1dfec`) add `internal/agent/retrieval_judge.go`: `retrieveContext` asks the engine for a `k*3` candidate pool via the new optional `ragCandidateProvider.QueryCandidates` seam, reranks it with the decider (type-asserted on demand, no new wiring) and injects the best k; it falls back to the engine's own ordering when there is no decider, the engine cannot list candidates, the pool is too small, or the judge errors, so a turn never fails and at most one judge call is made. `internal/rag/engine.go` gained `QueryCandidates` and a shared `formatChunk`. The S18 fixture ranking is explicitly a proxy measurement, not a real-model claim. Also `58573af` hardened the Ollama client after the cancelled subagent's hang was investigated: a default transport bounds dial (10s), TLS handshake (10s) and response headers (60s) while the overall request timeout stays 0, so a dead endpoint fails fast without cutting long streams. Evidence: gofmt clean; `go build`/`go vet` clean with and without `-tags rag`; `go test ./...` and `go test -tags rag ./...` both fully green. New tests: `internal/decision/relevance_test.go`, `internal/ollama/http_timeout_test.go`, `internal/agent/retrieval_judge_test.go`. Remaining T4: S17 (query rewrite on the sidecar) and S21 (estimator calibration).
