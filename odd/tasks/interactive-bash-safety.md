# InteractiveBash and SendInput Safety Hardening

Objective: close privilege and security bypasses in `SendInput` and interactive terminal sessions. Ensure `SendInput` is classified as `TierAsk`, subjected to dangerous command guards, command gravity classification, and domain boundary checks; and prevent persisting `allow` permissions for arbitrary command execution tools.

Branch: `main`
Delivery strategy: `ask-on-risk`
Test runner: `go test ./internal/permissions/... ./internal/tools/... ./internal/safety/...`

## Specs

- **S1** `SendInput` and `InteractiveBash` belong to `TierAsk` in `internal/permissions/tiers.go`. `SendInput` is removed from `safeTools`.
- **S2** `isArbitraryCommandTool` forbids persisting `allow` for `bash`, `interactivebash`, `sendinput`, and `backgroundrun` in `internal/permissions/persistence.go` (`mergePersistentMap`, `savePersistentLocked`) and `internal/permissions/manager.go` (`AllowAll`).
- **S3** `isShellTool` in `internal/permissions/gravity.go` includes `sendinput`. `manager.go` extracts the command string from `params["input"]` when `tool == "sendinput"`, so `CommandGravity` and offline-mode checks evaluate `SendInput` commands.
- **S4** `ToolTargetPaths` in `internal/permissions/domain.go` inspects `params["input"]` when `tool == "sendinput"`, extracting candidate paths so out-of-domain commands submitted via `SendInput` require domain approval.
- **S5** `SendInputTool.Execute` in `internal/tools/interactive_bash.go` checks `safety.IsDangerousCommand(input)`, `safety.IsProtectedWrite(input)`, and `safety.IsBackgroundCommand(input)` before sending input to the session.
- **S6** `safety.IsProtectedWrite` and `safety.IsProtectedPath` protect `vibe-coder.env`, `.ssh/authorized_keys`, `.ssh/config`, `.git/hooks`, and `.git/config`.

## Tasks

- [x] T1 permissions: classify `sendinput` as `TierAsk`, block persisting `allow` for arbitrary command tools (`bash`, `interactivebash`, `sendinput`, `backgroundrun`), wire `params["input"]` into `isShellTool` gravity and offline checks + tests — S1, S2, S3
- [x] T2 tools, safety: validate dangerous commands and protected writes in `SendInputTool.Execute`, wire domain path extraction for `sendinput`, and protect `vibe-coder.env` and sensitive ssh/git files + tests — S4, S5, S6
