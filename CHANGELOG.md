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

### Changed

- Styled startup banner now shows a colored wordmark with aligned runtime facts;
  plain output is unchanged for scripts and pipes.

### Fixed

- Applied `gofmt` to files that had drifted from the canonical format.

## [1.0.4] - 2026-09-14

See the [release notes](https://github.com/jonathanhecl/vibe-coder/releases/tag/v1.0.4).
