# vibe-coder

`vibe-coder` is a local-first coding agent for Ollama, built in Go.
It runs as a single CLI binary and supports one-shot prompts, interactive sessions, tools, permissions, session persistence, and optional RAG.

## Highlights

- Local-first by default (Ollama-based workflow).
- Interactive REPL plus one-shot mode (`-p`).
- Built-in tool system (`Read`, `Write`, `Edit`, `Glob`, `Bash`, `Grep`, web tools, notebook editing, tasks, subagents, and more).
- Session persistence with project-aware indexing and compaction.
- Optional RAG indexing and retrieval (build/runtime features already wired).
- Safety and permission layers for potentially dangerous operations.

## Requirements

- Go `1.25+`
- A running Ollama instance for model-backed execution

## Build

```bash
go build -o vibe-coder ./cmd/vibe-coder
```

Windows:

```powershell
go build -o vibe-coder.exe ./cmd/vibe-coder
```

## Quick Start

One-shot prompt:

```bash
./vibe-coder -p "Summarize this repository"
```

Interactive mode:

```bash
./vibe-coder
```

Use a specific model and host:

```bash
./vibe-coder --model llama3.1:8b --ollama-host http://127.0.0.1:11434
```

## CLI Flags

Current top-level flags:

- `--version` print version and exit
- `--help` show help
- `-p` one-shot prompt
- `-m, --model` model name
- `-y` yes mode
- `--debug` debug logs
- `--resume` resume last project session
- `--session-id` resume a specific session
- `--list-sessions` list known sessions
- `--ollama-host` Ollama base URL
- `--max-tokens` max generated tokens
- `--temperature` sampling temperature
- `--context-window` model context window
- `--rag` enable RAG mode
- `--rag-mode` RAG mode
- `--rag-path` RAG path
- `--rag-topk` RAG top-k chunks
- `--rag-model` RAG embedding model
- `--rag-index` build/index RAG path and exit

## RAG Usage

Build an index:

```bash
./vibe-coder --rag-index ./somewhere
```

Run with RAG enabled:

```bash
./vibe-coder --rag -p "Find where permissions are enforced"
```

## Development

Run tests:

```bash
go test ./...
```

Run tests with RAG tag:

```bash
go test -tags rag ./...
```

Release snapshot with GoReleaser:

```bash
go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=publish
```

## Project Docs

For architecture, roadmap, and verification checkpoints:

- `doc/README.md`
- `doc/MVP.md`
- `doc/CHECKLIST.md`
- `doc/docs/`