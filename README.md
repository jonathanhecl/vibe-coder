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

## Install

From source in this repository:

```bash
go build -o vibe-coder ./cmd/vibe-coder
```

Install directly with `go install`:

```bash
go install github.com/jonathanhecl/vibe-coder/cmd/vibe-coder@latest
```

If your `GOBIN`/`GOPATH/bin` is in `PATH`, run:

```bash
vibe-coder --version
```

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

## Model Configuration

Model settings are loaded with this precedence:

1. defaults
2. config file
3. environment variables
4. CLI flags (highest priority)

Default config file path:

- Windows: `%LOCALAPPDATA%\vibe-coder\config.env`
- Linux/macOS: `~/.config/vibe-coder/config.env`

You can override the config file path with:

- `VIBE_CODER_CONFIG=<path>`

Model keys and overrides:

- Config file key: `MODEL=<model-name>`
- Config file key: `SIDECAR_MODEL=<model-name>`
- Environment: `VIBE_CODER_MODEL=<model-name>`
- Environment: `VIBE_CODER_SIDECAR_MODEL=<model-name>`
- CLI: `--model <model-name>` (or `-m <model-name>`)

If no model is set, `vibe-coder` auto-selects one based on detected RAM tier.

### Remote Ollama for vibe-coder only

If Ollama runs on another machine in your network, you can configure `vibe-coder` and persist
those settings in one command, without changing global environment variables:

```powershell
.\vibe-coder.exe -model "qwen2.5-coder:7b" -sidecar "llama3.2:3b" -ollama-host "http://192.168.1.50:11434" /save
```

What this does:

- Applies model, sidecar model, and host for the current run.
- Writes `MODEL`, `SIDECAR_MODEL`, and `OLLAMA_HOST` to
  `%LOCALAPPDATA%\vibe-coder\config.env`.
- Keeps the change scoped to `vibe-coder` only (no `setx` needed).

Next runs can simply use:

```powershell
.\vibe-coder.exe
```

## CLI Flags

Current top-level flags:

- `--version` print version and exit
- `--help` show help
- `-p` one-shot prompt
- `-m, --model` model name
- `--sidecar` sidecar model name
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
- `/save` persist current model/sidecar/host into `config.env`

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
