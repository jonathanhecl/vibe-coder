# Harness CLI tunables and A/B metrics

Objective: make a harness change measurable from the command line — the three harness tunables reachable as flags, and `vibe-metrics --diff` reporting the delta between a baseline group and a candidate group instead of only aggregating each file.

Branch: `main` (staged work-unit commits, no worktree — matches `odd/tasks/lsp-diagnostics.md`)
Delivery strategy: `ask-on-risk`
Test runner: `go test ./...`

## Specs

- **S1** `KEEP_TOOL_RESULTS`, `TOOL_OUTPUT_MAX_TOKENS` and `COMPACT_AT_PERCENT` are also settable as `--keep-tool-results`, `--tool-output-max-tokens` and `--compact-at-percent` (`internal/config/flags.go`), applied in `applyCLI` so the CLI keeps winning over the config file and the environment.
- **S2** `Config.Harness.normalize()` runs after `applyCLI` in `config.Load` (`internal/config/config.go`), so a value typed on the command line cannot bypass the documented ranges. This replaces the previous order, where harness tunables normalized before the CLI because nothing set them from there.
- **S3** `vibe-metrics --diff <baseline> <candidate>` (`cmd/vibe-metrics`) expands each side (file or directory), folds every entry through `metrics.Summarize`, and prints one row per metric with baseline, candidate, absolute delta and percentage change, followed by each side's file and request counts. A zero baseline prints `n/a`; an unreadable side fails with exit 1 rather than diffing fewer files than were named; a wrong arity exits 2 with the usage line.
- **S4** The existing summarize mode keeps its columns, its `TOTAL` row and its lenient per-file error handling, so every current caller and the documented output stay valid.

## Tasks

- [x] T1 config: register the three harness flags, apply them in `applyCLI`, move `Harness.normalize()` after the CLI and cover precedence plus clamping with `Load`-level tests — S1, S2 — `095d701`
- [x] T2 metrics CLI: add `--diff` with `run`/`derive`/`diffRows`, percent-change handling for a zero baseline, fail-closed group loading and tests for both modes — S3, S4 — `0f1d770`
- [x] T3 docs: README flag list, harness-tunables precedence line, `--diff` section; CHANGELOG entry; this task record — S1, S3 — `095d701`, `0f1d770`

## Log

- L1 (user, verbatim): "dale, haz el 2 y 3" — the two items proposed after reviewing the harness: CLI-settable tunables and a diff mode for `vibe-metrics`.
- D1 (decision): flags before the diff, because a diff only says something when the two runs differ by exactly one known switch; an A/B driven by exported environment variables is not reproducible from the command line recorded next to the numbers.
- D2 (decision): the diff compares groups of files, not two sessions, and reports per-request averages beside the totals. Two runs of different length are the normal case, and totals alone would read as a regression whenever the candidate simply ran longer.
- D3 (decision): the diff fails closed on an unreadable group while the summarize mode keeps printing the files it could read. A partial group silently produces a confident wrong delta; a partial summary is still an honest summary.
- D4 (decision): a zero baseline prints `n/a`, not `+100%` or a division-by-zero guard that renders `0%`. The first request of a fresh harness has no percentage, and saying so is the only reading a later comparison will not misquote.
- D5 (observed): the config layer's normalization order was the load-order half of this change, so it landed with the flags rather than as a separate commit: a flag whose value escapes clamping is a half-feature.
- L2 (finding): `go test ./...` is red only in `internal/slash`, at `TestNoteUserProvidedPaths_ReachesDecisionModel`, and it was red before this work. The test builds its permission manager from a `config.Config` literal without `Cwd`, so `NewManager` falls back to `os.Getwd()` and the workspace domain becomes whatever directory the test binary runs in. In a real checkout `ls -l <tmpfile>` classifies as `GravityRead`, `Manager.Check` approves it without consulting the safety decider, and the note the test inspects stays empty; from `/tmp` the same compiled binary passes. Proven by running one `go test -c` binary from two working directories and by reproducing at `HEAD` in a non-temp clone. Unrelated to S1–S4 and left alone here; the fix is either an explicit workspace that does not contain the pasted paths or a test action that cannot short-circuit as a read.
