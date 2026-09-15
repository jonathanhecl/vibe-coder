# Contributing to vibe-coder

Thanks for your interest in improving `vibe`. This guide covers the local setup,
conventions, and the review process. By participating you agree to the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Ways to contribute

- Report bugs with the [bug report template](.github/ISSUE_TEMPLATE/bug_report.yml).
- Propose features with the [feature request template](.github/ISSUE_TEMPLATE/feature_request.yml).
- Improve documentation, tests, or the tool implementations.
- Review open pull requests and reproduce reported issues.

Security issues must **not** be filed publicly — see [SECURITY.md](SECURITY.md).

## Requirements

- Go `1.26+` (the exact version is in [`go.mod`](go.mod)).
- A running [Ollama](https://ollama.com) instance for manual, model-backed testing.
- Optional: a zip tool (`zip`, 7z, `bsdtar`, or `python3`) for release packaging.

## Development setup

```bash
git clone https://github.com/jonathanhecl/vibe-coder.git
cd vibe-coder

go build -o vibe ./cmd/vibe     # or: ./run.sh
./vibe --version
```

The optional RAG engine is behind a build tag:

```bash
go build -tags rag -o vibe ./cmd/vibe
```

## Tests

Every functional change needs a test. Run the default suite before opening a PR:

```bash
gofmt -l .                      # must print nothing
go vet ./...
go test ./...
go test -tags rag ./...         # RAG-enabled build
```

Hermetic end-to-end scenarios (anti-loop with an in-process fake Ollama, plus CLI
flows) run inside `go test`. The longer, model/network-driven harnesses under
[`e2e/`](e2e/README.md) are run manually — see that README for coverage and
instructions.

## Coding conventions

These mirror [`AGENTS.md`](AGENTS.md) and apply to all contributions:

- **English only** — code comments, user-facing messages, logs, errors, CLI help,
  and test fixtures.
- **Binary name** — `vibe` in new code and commands (the module stays `vibe-coder`).
- **Config precedence** — defaults `<` config file `<` environment `<` CLI flags.
- **Ollama endpoints** — native `/api/chat`, `/api/tags`, `/api/version`.
- **Small packages** — prefer focused packages under `internal/`; keep
  `cmd/vibe/main.go` limited to wiring.
- **Keep it small** — MVP-first: the smallest change that is correct and testable,
  documented in the README/CHANGELOG when behavior changes.
- Do not add comments that merely restate the code.

## Commit and PR style

Commits follow [Conventional Commits](https://www.conventionalcommits.org/):
`feat:`, `fix:`, `docs:`, `test:`, `chore:`, `refactor:`, `ci:`.

A pull request should:

1. Stay focused on one change and avoid unrelated reformatting.
2. Include tests and a clear description of the motivation and behavior change.
3. Update the README and/or [`CHANGELOG.md`](CHANGELOG.md) for user-visible changes.
4. Pass `gofmt -l .`, `go vet ./...`, and `go test ./...` locally.

CI runs the same checks plus a cross-compilation matrix for every supported
platform. Releases are cut from `main` with
[`release.sh`](release.sh) / [`release.ps1`](release.ps1); tagging is handled by
the maintainer.

## License

By contributing, you agree that your contributions are licensed under the
[MIT License](LICENSE).
