# Task: fix the failing tests with minimal edits

The Go project in the working directory has failing tests. Find the bugs and
fix them.

## Environment

- The project root is `__PROJECT_DIR__`.
- Run `go test ./...` there to see the failures.

## Rules

- Fix the bugs in the non-test source files (`calc/calc.go`, `text/text.go`).
- Change ONLY those two source files. Do NOT modify `*_test.go` files,
  `go.mod`, or `NOTES.md`, and do not create new files.
- Prefer precise `Edit` operations; keep changes minimal.
- After each change, re-run `go test ./...` until everything passes.
- When `go test ./...` passes, report the fixes you made.
