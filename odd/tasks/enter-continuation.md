# Enter Continuation

Branch: `main` · Delivery: `single-pr` · Runner: `go test ./...`

Make a bare Enter resume paused work from a durable checkpoint without adding a user message, while a typed message remains a normal correction.

## Specs

S1. At the interactive prompt, when resumable work exists, pressing Enter with an empty line resumes the active work from its checkpoint without adding a user message to the session transcript.

S2. When no resumable work exists, an empty line remains a no-op; it must not start a request, a slash command, or a mission turn.

S3. A typed message always remains a normal user request and may correct or replace the active work.

S4. The durable work state stores a compact resume checkpoint with the active goal, the exact next action, and the stop reason. It survives `RestoreWorkState` and session persistence, and is cleared when no resumable work remains.

S5. The continuation run derives its instruction from the checkpoint in ephemeral runtime context: continue from the next action, do not restate the plan, do not repeat completed work, and read only the artifact needed for the next step. The instruction is not stored as a user message.

S6. When the paused work belongs to a blocked mission, resuming through Enter reactivates that mission before the run and then uses the existing autonomous mission loop.

S7. Stop and pending-work messages say `Press Enter to continue; type a correction instead.` They no longer require typing `continue`, while existing typed continuation shorthands keep working.

S8. Continuation is explicit: a cancelled, blocked-by-policy, or hard-failed turn still returns control to the caller. Nothing resumes automatically without Enter.

## Tasks

- [x] T1: Add checkpoint capture/restore and `CanResume`/`RunContinuation` agent APIs. Specs: S1, S4, S5, S6, S8. Evidence: `internal/agent/resume.go`, `internal/agent/workstate.go`, `internal/tools/mission.go`; tests `TestWorkStatePersistsResumeCheckpoint`, `TestRunContinuationAddsNoUserMessageAndCarriesCheckpoint`, `TestRunContinuationReactivatesBlockedMission`, `TestRunContinuationWithoutTranscriptCarriesCheckpoint`, `TestIterationCapWithoutChecklistLeavesResumeCursor`.
- [x] T2: Wire empty Enter in the REPL to the mission-aware continuation path. Specs: S1, S2, S3, S6. Evidence: `cmd/vibe/repl.go` (`runInteractiveREPL`, `runContinuationPrompt`, `runContinuationWithRetry`); test `TestContinuationPromptStoresNoEmptyUserMessage`.
- [x] T3: Update stop messages, tests, README, and CHANGELOG. Specs: S7. Evidence: README durable-state and mission sections, CHANGELOG Unreleased Added, updated stop/pending text and assertions. Commit: `5b87426`.

## Log

L1 (user, verbatim): "okay, hazlo, mejoralo. cuando se pause, me deje continuar haciendo ENTER sin escribir nada. esto le ayuda al harness a entender que SI O SI esta pidiendo continuar en lo que estaba. porque actualmente debo escribir \"continue\" y esto interrumpe al modelo poniendolo a pensar en lo que he escrito. esta bien si quiero usar al pausa para dar alguna correccion, pero si quiero continuar de verdad, no poner nada y ENTER deberia servir para no interrumpir el pensamiento del agente."

L2 (user, earlier, verbatim): "si se me pausa a mitad de iteracciones, poder continuarlo, sin interrumpir es fundamental"

L3: Implementation started on `feat/enter-continuation`. Test-first path chosen for the checkpoint and continuation APIs, then REPL dispatch and message updates.

L4: Implemented. `go test ./cmd/vibe ./internal/agent ./internal/tools` passes; `go test $(go list ./... | grep -v '/internal/slash$')` passes; `go vet ./...` passes; `go test -race ./internal/agent -run 'Continuation|Resume|WorkState'` passes. `go test ./...` still fails in pre-existing `internal/slash` `TestNoteUserProvidedPaths_ReachesDecisionModel` (the same test fails on a clean `HEAD` worktree), so it is recorded as an environmental baseline failure, not caused by this change.

L5: Review note: continuation is explicit (Enter), not automatic after a hard failure. Pending TODO/mission work flushes after each successful tool; a completed run clears the cursor.

L6: Work unit committed as `5b87426` directly on `main`.

L7: Follow-up guard committed as `5808463`. Native assessment over `261edae..HEAD` returned `medium` risk and `under_budget` (no review due). `go build ./...` passes.
