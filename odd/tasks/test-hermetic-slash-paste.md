# Hermetic slash paste test

Objective: close the one red test in the suite — `internal/slash`'s `TestNoteUserProvidedPaths_ReachesDecisionModel` passed or failed depending on where the test binary ran — so `go test ./...` is green from any checkout path.

Branch: `main`
Delivery strategy: `ask-on-risk`
Test runner: `go test ./...`

## Specs

- **S1** The test supplies its own workspace instead of inheriting `os.Getwd()`: the permission manager is built with `Cwd: t.TempDir()`, a directory that does not contain the pasted paths, so classification does not depend on the checkout location.
- **S2** The behaviour under test is unchanged: a pasted path outside the workspace domain is registered on the manager and forwarded to the decision model as part of the assisted-mode note.
- **S3** `go test ./...` passes from the package directory and from any other working directory, since the same compiled binary is no longer sensitive to cwd.

## Tasks

- [x] T1 `fc10280` slash: pass an explicit `Cwd` to the manager in `TestNoteUserProvidedPaths_ReachesDecisionModel`, with a comment stating why — S1–S2
- [x] T2 verification: run the package test and the compiled binary from two working directories — S3
- [x] T3 docs: close the finding recorded in `odd/tasks/harness-cli-ab.md` (L2), this record — S3

## Log

- L1 (user, verbatim): "okay, dejemos esto prolijo. sin pendientes." — the documented red test was the only pending item on the board.
- D1 (decision): fix the test, not the manager. `NewManager` falling back to `os.Getwd()` is reasonable for a real run started in a workspace; the flaw is a test that forgot to say which workspace it was testing, so the fixture carries the fix.
- D2 (decision): a sibling temp dir as the workspace, not the pasted files' own directory. Pointing `Cwd` at the directory holding the pasted paths would make them *inside* the domain, which is the opposite of the case the test documents.
- D3 (evidence): before the change, one `go test -c` binary passed from `/tmp` and failed from the repo; after it, the same binary passes from both, and `go test ./...` is green.
