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
- [ ] T3 memory: compaction demotes to vector store + goal-based gated retrieval — S12–S14 — route: pending
- [ ] T4 judge: decision relevance/dedupe + fallback chain + query rewrite + leftovers — S15–S21 — route: pending

## Log

- L1 (user, verbatim): "ahora podemos ver como hacer un uso mas optimo de los modelos, esta bien tener un main y un sidecar? podemos usar embedding en el rag? el rag puede ser inteligente? puede ser temporal y usarse como contexto largo para cuando tiene que hacer tareas largas? que opinas?"
- L2 (user, verbatim): "podriamos tener un sidecar especifico dedicado al manejo de la memoria, del rag, etc? que opinas? imaginando que podriamos usar modelos eficientes enfocados en estas funciones"
- L3 (user, verbatim): "tambien tenemos modelo de decision, que podría tomar tareas de juez ya que es muy veloz. hay modelos superveloces pero no son tan inteligentes en ciertas tarea a menos que se reentrenen para tareas concretas. igualmente es una idea valida"
- L4 (user, verbatim): "como queda la idea finalmente, que vamos a hacer?"
- L5 (user, verbatim, authorization): "esta perfecto. si, inicialo, haz commits de forma regular a medida q avancemos, todo branch main. como fallback si no hay modelo decision, usar sidecar, sino hay sidecar cae en main. si no hay embedding será rag en texto."
- L6 (decisions): role count stays at five; the only new leg is the embedding encoder consumer. Judgment reuses the decision model; encoder reuses the embedding role. Deterministic work (chunking, gating, hashing, path truncation) is moved off the LLM. Sequence: T1 → T2 → T3 → T4.
- L7 (progress, T1, commit a0d7f69): per-role keep_alive shipped. `internal/config`: `KeepAliveMain`/`KeepAliveWorker` + defaults -1/300 + `KeepAliveFor(role)` + `KEEP_ALIVE_MAIN`/`KEEP_ALIVE_WORKER` file keys, `VIBE_CODER_KEEP_ALIVE_*` env and `--keep-alive-main/worker` flags + persistence + help text. `internal/ollama`: `keep_alive,omitempty` and the unconditional 0→-1 rewrite removed, so 0 now means "provider default". Call sites send their role value: main chat, subagent, `/commit` and main image description → main; sidecar chat/description → sidecar; compaction → sidecar-or-main as actually used; vision role → vision. Evidence: `gofmt -l` clean on touched files; `go vet ./...` silent; `go build ./...` exit 0; `go test ./...` fully green (config 0.301s, ollama 0.387s, sidecar 0.320s, visionrole 0.242s, tools 2.011s, session 0.356s, agent 18.597s, slash 0.535s, cmd/vibe 24.064s). New tests: `internal/ollama/keepalive_test.go` (marshal 0 omits / -1 and N sent; Chat preserves the value) and `internal/config/keepalive_test.go` (defaults, precedence file<env<CLI, nil-safe accessor, `--save` persistence). README and CHANGELOG updated.
- L8 (progress, T2, commits 23fc9d9 + 247a017 + b5bd1d7): hybrid RAG shipped. T2a `internal/ollama/embed.go` adds a batched `/api/embed` client behind a narrow exported `Embedder` interface (kept out of the `Client` interface so the OpenAI adapter and six test stubs stay untouched); `*HTTPClient` satisfies it, `*openai.Client` does not → text-only fallback. T2b rewrote `internal/rag/engine.go`: a `files(path,hash)` table plus an `embedding` column (auto-`ALTER`-migrated) make indexing incremental by content hash and prune vanished files; chunks are split at declaration boundaries (`splitCodeChunks` in `text.go`) and embedded in batches of 32; retrieval ranks lexically and by vector cosine then fuses with RRF (k=60), and any embedder error degrades to lexical-only without failing the index. T2c removed the legacy flat `RAGModel`/`--rag-model` and wired the encoder from the `embedding` role via `embeddingClientFor` (mirrors `visionClientFor`, returns the main client when unbound). Evidence: `gofmt -l` clean on touched files (three pre-existing unformatted files left untouched); `go build`/`go vet` clean with and without `-tags rag`; `go test ./...` fully green and `go test -tags rag ./...` fully green (rag 0.401s). New tests: `internal/ollama/embed_test.go` (batch POST, auth header, model/inputs, mismatched vectors, HTTP error) and `internal/rag/index_test.go` (symbol chunking, incremental + prune, embedder-failure degradation, hybrid vector hit via RRF, legacy-schema migration). README and CHANGELOG updated.
