# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Release archives and generated notes are available on the
[GitHub Releases](https://github.com/jonathanhecl/vibe-coder/releases) page.

## [Unreleased]

### Added

- Branded repository assets (`assets/logo.svg`, `assets/logo-mark.svg`) and a
  refreshed README header with badges and a table of contents.
- Continuous integration workflow (formatting, vet, tests, RAG-tagged tests, and
  a cross-compilation matrix), issue/PR templates, and Dependabot updates.
- `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, and this changelog.
- JEV Style decision model configuration support across CLI flags (`--jevstyle-model`, `--jevstyle`), environment variables (`VIBE_CODER_JEVSTYLE_MODEL`, `VIBEGO_JEVSTYLE_MODEL`), config file (`JEVSTYLE_MODEL`), `/jevstyle` slash command, and `--save` persistence.
- JEV Style assisted command execution mode (`--assisted-yes`, `/yes assisted`, `/jevstyle assisted on`) allowing automatic approval of safe commands while prompting for dangerous actions. Includes config file persistence (`ASSISTED_YES`), startup banner indicator (`(assisted enabled)` in green when active, `(run '/yes assisted' to enable)` in white when inactive), auto-deactivation and graceful fallback to manual approval if the model is absent or errors, and setup suggestion tips when configuring JEV Style models.
- Agent tool `JevDecide` exposed to the primary coding model and sidecars to query the JEV Style decision model for second opinions, safety validations, and discrete choices.
- Automatic session resume: starting `vibe` in a directory automatically loads the most recent session for that directory, displaying `(resumed)` next to the session ID in the startup banner. Added `-n`/`--new` CLI flag to bypass auto-resuming and start fresh.
- Session origin project path tracking: saved sessions persist the directory where they were initialized (`project_path` in `<id>.ctx.json` sidecar and `session-projects.json` fast index), and `/sessions` displays a formatted `PATH` column with home-relative (`~`) shorthand and directory hierarchy preservation.
- Decision workflows powered by JEV Style: workspace path disambiguation, autonomous mission goal completion verification, tool failure root-cause diagnosis hints, and conventional commit type classification.
- Temporal (ephemeral) session mode (`-t`, `--temporal`, `--temp`, `VIBE_CODER_TEMPORAL`): runs an ephemeral session where conversation history, transcripts, and session indexes are discarded on exit, while files and workspace changes made by the agent remain on disk. Includes red `(temporal)` banner status, `/new` reset support, and promotion to a permanent session with `/promote`.
- Isolated session mode (`--isolated`, `--isolate`, `VIBE_CODER_ISOLATED`, `ISOLATED`): runs a project-local session stored in a single `.vibe-isolated.jsonl` file strictly within the working directory, inaccessible to other folders and omitted from global session state. Automatically detects and loads the folder's isolated session on startup (with yellow `(isolated)` banner status), and resets/truncates the file on `/new`.
- First-run onboarding now also offers the optional JEV Style decision model, alongside the primary and sidecar models, and shows it in the closing summary.
- Numbered model pickers for `/model`, `/sidecar`, and `/jevstyle`: each lists the installed models once with capability tags and lets you choose by number (interactively or as an argument, e.g. `/model 2`), type a model name, press Enter to keep the current selection, or `[0]` to disable the sidecar/JEV Style role.
- `/models` lists every installed model, numbered and tagged, marks which one each role currently uses, and explains how to switch with `/model <n>`, `/sidecar <n>`, or `/jevstyle <n>` and persist with `/save`.
- `/promote` (alias `/keep`) keeps a temporal session by promoting it to a permanent, saved session.

### Changed

- `/save` now persists model/thinking settings and no longer promotes a temporal session automatically; use `/promote` to keep a temporal session. Settings changed by `/model`, `/sidecar`, `/jevstyle`, `/think`, and `/hide-think` are still persisted with `/save`.
- First-run onboarding lists the installed models only once and reuses the same numbering for the primary, optional sidecar, and optional JEV Style prompts (previously the tool-capable list was printed again for each role). Entries are tagged `recommended`/`tools`/`vision`/`thinking`, and the primary selection rejects models that do not report tool support.
- Empty sessions with 0 messages are no longer persisted to disk, preventing clutter from aborted or empty CLI/REPL runs. `/sessions` and `ListSessions` automatically skip and clean up stale 0-message session files.
- Fixed Ollama `temperature` option serialization to prevent omitting `0` for deterministic decision models.

- Styled startup banner now shows a colored wordmark with aligned runtime facts;
  plain output is unchanged for scripts and pipes.

### Fixed

- Slash command tests now write settings to a temporary directory instead of creating a `vibe-coder.env` artifact in the package; the stray tracked file was removed.
- Interactive input no longer desyncs the terminal cursor. Two root causes were
  fixed: (1) `insertRune` advanced the tracked screen column *before* redrawing,
  so every mid-line insert made `redraw` move one cell too far left, shifting
  the text and erasing the space after `user >`; (2) width was measured in runes,
  not terminal cells, so the double-width `👤` prompt icon (and any emoji/CJK
  typed by the user) threw the cursor off by one. Moving with the arrow keys no
  longer overlaps letters, typing lands at the cursor, and backspace can no
  longer erase the prompt. Wide runes are erased by both cells they occupy.
- Restored the macOS build of the TUI: terminal attributes use the Darwin/BSD
  `TIOCGETA`/`TIOCSETA` ioctls instead of the Linux-only `TCGETS`/`TCSETS`.
- Applied `gofmt` to files that had drifted from the canonical format.
- Removed superfluous blank lines and orphan prefix bars in thinking blocks and assistant turn completion footers (`thought for Xs` and `responded in Xs`).

## [1.0.4] - 2026-09-14

See the [release notes](https://github.com/jonathanhecl/vibe-coder/releases/tag/v1.0.4).
