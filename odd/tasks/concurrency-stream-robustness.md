# Task: Concurrency and Stream Robustness

Status: in-progress

## Context
Auditing identified race conditions and unhandled stream errors:
1. `internal/background/manager.go` called `cmd.Wait()` before `wg.Wait()`, prematurely closing pipes while `copyStream` is draining.
2. `internal/background/task.go` called `t.Elapsed()` inside `t.Snapshot()` and `t.FormatStatusBanner()` while holding `t.mu.RLock()`, causing potential recursive read-lock deadlocks when writers queue.
3. Unsynchronized field reads (`task.Status`, `task.ExitCode`, etc.) in `internal/tools/background.go` and `cmd/vibe/main.go`.
4. `internal/tools/interactive_bash.go` read `sess.ExitCode()` before calling `t.mgr.Terminate(sessionID)`, always reporting -1 for running sessions.
5. `internal/openai/client.go` had an inert stall timer (`time.NewTimer`) with no select loop while `scanner.Scan()` blocked, and missed checking `scanner.Err()` on stream finish.

## Tasks
- [x] T1: Fix background task stream drain ordering in `internal/background/manager.go`.
- [x] T2: Eliminate recursive read lock in `internal/background/task.go` and use `task.Snapshot()` in `internal/tools/background.go` and `cmd/vibe/main.go`.
- [x] T3: Fix session termination exit code reporting in `internal/tools/interactive_bash.go`.
- [x] T4: Implement active stall timer and scanner error check in `internal/openai/client.go`.
- [x] T5: Write regression tests and verify full test suite passes.
