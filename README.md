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

Windows helper script:

```powershell
.\build.ps1
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

### What is the sidecar model for?

`MODEL` is the conversational/coding model that answers every prompt. The
**sidecar** is a smaller, faster model `vibe-coder` uses internally for
short, high-leverage tasks the main model would either bloat the context
with or answer too slowly. All sidecar calls are guarded by a worker
semaphore, request deduplication (`singleflight`) and a small LRU cache,
so even on a single local Ollama instance you never see N parallel
requests piling up.

The sidecar is invoked in three places today:

1. **Session compaction** — when the session has more than 300 messages
   or the rolling token estimate exceeds 70% of `ContextWindow`,
   `Session.Compact()` sends the oldest messages to the sidecar with a
   "Summarize the conversation concisely" prompt and replaces them with
   the summary. The last 30 messages are kept verbatim.
2. **Tool-output condensation** — when a tool (typically `Read`, `Bash`,
   `Grep`) returns more than ~6 KB, the output is sent to the sidecar
   with a strict "produce 4-10 bullets, preserve paths/symbols/errors,
   no prose" system prompt. The condensed bullets replace the raw bytes
   in the model's context (the unredacted output is still printed live
   in the TUI). This keeps the main model focused and dramatically
   reduces tokens-per-turn on big files.
3. **Path disambiguation** — when the agent rescues a relative path
   (e.g. `Read("config.go")`) and finds **multiple** known absolute
   candidates, the sidecar picks one based on the user's current goal
   (`PICK: <n>` reply). If it declines or is unavailable, the rescue is
   skipped, matching the previous behaviour.

The TUI spinner (`waiting for <MODEL>…`) is always the **main** model;
sidecar calls are short and run in the background so you only see them
through tool-result hints like `sidecar condensed 12345 bytes → summary
stored in context` or `sidecar disambiguated "config.go" → <abs>`.

Pick a sidecar that is **fast and cheap** (e.g. `llama3.2:3b`,
`qwen3.5:4b`, `phi3:mini`). Leave it empty to disable all three
behaviours: compaction will truncate to a static "Earlier conversation
truncated…" note, large tool outputs will be inserted verbatim into the
context, and ambiguous paths will not be rescued.

### Remote Ollama for vibe-coder only

If Ollama runs on another machine in your network, you can configure `vibe-coder` and persist
those settings in one command, without changing global environment variables:

```powershell
.\vibe-coder.exe -model "qwen3.5:9b" -sidecar "qwen3.5:4b" -ollama-host "http://192.168.1.50:11434" /save
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

If you use PowerShell and want to run from source with the same flags:

```powershell
.\run.ps1 -model "qwen3.5:9b" -sidecar "qwen3.5:4b" -ollama-host "http://192.168.1.50:11434" /save
```

## CLI Flags

Current top-level flags:

- `--version` print version and exit
- `--help` show help
- `-p` one-shot prompt
- `-m, --model` model name
- `--sidecar` sidecar model name (`SIDECAR_MODEL` in `config.env`)
- `--no-sidecar` do not use the sidecar; with `/save`, writes `SIDECAR_DISABLED=true` to `config.env` so it stays off on future runs
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
- `/save` persist `MODEL`, `SIDECAR_MODEL`, and `OLLAMA_HOST` into `config.env`; if combined with `--no-sidecar`, also persists `SIDECAR_DISABLED=true`

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
