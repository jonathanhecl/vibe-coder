# RAG Project Isolation and Lifecycle Management

Objective: isolate SQLite-backed RAG databases per workspace to prevent cross-project index destruction, and ensure proper lifecycle cleanup on session exit.

Branch: `main`
Delivery strategy: `ask-on-risk`
Test runner: `go test -tags rag ./internal/rag/...`

## Specs

- **S1** `NewEngine` in `internal/rag/engine.go` isolates the default database per workspace directory under `<StateDir>/rag/<projectKey>.db`, using `sha256(abs(cwd))[:16]` to match session persistence conventions. Indexing project B no longer purges chunks from project A.
- **S2** `Engine.Path()` exposes the resolved database path.
- **S3** `configureRAG` in `cmd/vibe/rag_enabled.go` and `cmd/vibe/rag_disabled.go` returns a cleanup function so `main.go` defers `engine.Close()` on session exit, preventing orphaned SQLite WAL locks and file descriptor leaks.
- **S4** Unit tests in `internal/rag/engine_test.go` verify that different workspaces resolve distinct database paths and that paths match the expected hash.

## Tasks

- [x] T1 rag: project-isolated database path in `StateDir/rag/<projectKey>.db` + `Path()` getter + tests — S1, S2, S4
- [x] T2 cmd/vibe: deferred lifecycle close of RAG engine in `configureRAG` / `main.go` — S3
