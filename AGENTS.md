# AGENTS.md

## Project Defaults

- Use English for all code comments, user-facing messages, logs, errors, and test fixtures.
- Use `vibe-coder` as the canonical binary and package naming in new code and commands.
- Follow MVP-first scope discipline from `doc/MVP.md` and `doc/CHECKLIST.md`.
- Prefer small, testable packages under `internal/` and keep `cmd/vibe-coder/main.go` focused on wiring.

## Language and Messaging Rules

- Never introduce Spanish (or other non-English) strings in runtime output.
- Error messages must be clear, actionable, and in English.
- CLI help text, status text, and fallback messages must be in English.

## Implementation Conventions

- Keep configuration precedence as: defaults < config file < env < CLI.
- Keep Ollama integration on native endpoints (`/api/chat`, `/api/tags`, `/api/version`).
- Keep one-shot behavior simple: `-p` runs once, streams output, exits cleanly.
- Add or update tests for every functional change.

## Documentation and Tracking

- Mark checklist items only after tests/build pass locally.
- When docs and code differ, keep code behavior aligned with current project decisions and update docs/checklist wording accordingly.

