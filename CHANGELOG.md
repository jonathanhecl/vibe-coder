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
- JEV Style assisted command execution mode (`--assisted-yes`, `/yes assisted`, `/jevstyle assisted on`) allowing automatic approval of safe commands while prompting for dangerous actions. Includes config file persistence (`ASSISTED_YES`), auto-deactivation and graceful fallback to manual approval if the model is absent or errors, and setup suggestion tips when configuring JEV Style models.
- Agent tool `JevDecide` exposed to the primary coding model and sidecars to query the JEV Style decision model for second opinions, safety validations, and discrete choices.
- Automatic session resume: starting `vibe` in a directory automatically loads the most recent session for that directory, displaying `(resumed)` next to the session ID in the startup banner. Added `-n`/`--new` CLI flag to bypass auto-resuming and start fresh.
- Session origin project path tracking: saved sessions persist the directory where they were initialized (`project_path` in `<id>.ctx.json` sidecar and `session-projects.json` fast index), and `/sessions` displays a formatted `PATH` column with home-relative (`~`) shorthand and directory hierarchy preservation.
- Decision workflows powered by JEV Style: workspace path disambiguation, autonomous mission goal completion verification, tool failure root-cause diagnosis hints, and conventional commit type classification.

### Changed

- Empty sessions with 0 messages are no longer persisted to disk, preventing clutter from aborted or empty CLI/REPL runs. `/sessions` and `ListSessions` automatically skip and clean up stale 0-message session files.
- Fixed Ollama `temperature` option serialization to prevent omitting `0` for deterministic decision models.

- Styled startup banner now shows a colored wordmark with aligned runtime facts;
  plain output is unchanged for scripts and pipes.

### Fixed

- Applied `gofmt` to files that had drifted from the canonical format.

## [1.0.4] - 2026-09-14

See the [release notes](https://github.com/jonathanhecl/vibe-coder/releases/tag/v1.0.4).
