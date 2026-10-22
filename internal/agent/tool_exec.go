package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/logger"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func (a *Agent) executeTool(ctx context.Context, tool tools.Tool, toolName string, toolParams map[string]any, mode toolExecutionMode) (tools.Result, bool, error) {
	if err := ctx.Err(); err != nil {
		return tools.Result{}, false, err
	}
	toolName = tool.Name()
	logger.Infof("Tool execution request: tool=%s, params=%+v", toolName, toolParams)
	a.rescuePathParam(ctx, toolName, toolParams)
	if blockMsg := a.toolModeBlock(tool, toolParams); blockMsg != "" {
		logger.Errorf("%s", blockMsg)
		a.ui.ShowToolResult(toolName, blockMsg, true, toolParams)
		if !mode.skipSessionRecord {
			if mode.structured {
				a.sess.AddToolResult(toolName, blockMsg)
			} else {
				a.sess.AddSystemNote(blockMsg)
			}
		}
		return tools.Result{Output: blockMsg, IsError: true}, false, nil
	}
	// While a mission is active the run is unattended: auto-approve Ask/Network
	// tools so it never blocks on a prompt. Dangerous commands are still denied.
	if a.perm != nil {
		a.perm.SetUnattended(a.MissionActive())
	}
	if !a.perm.Check(toolName, toolParams, a.ui) {
		deny := permissionDeniedNote(a.perm)
		logger.Errorf("Tool %s execution denied by permissions", toolName)
		if mode.showPermissionDeniedResult {
			a.ui.ShowToolResult(toolName, deny, true, toolParams)
		}
		if !mode.skipSessionRecord {
			if mode.structured {
				a.sess.AddToolResult(toolName, deny)
			} else {
				a.sess.AddSystemNote(deny)
			}
		}
		if mode.endAssistantOnDenied {
			a.ui.EndAssistant(a.turnUsage())
		}
		return tools.Result{Output: deny, IsError: true}, false, nil
	}

	if toolName == "Write" || toolName == "Edit" {
		// Create a checkpoint before mutating files so failed edits can be
		// inspected or rolled back by the user outside the agent loop.
		// The snapshot covers only the edited file; the working tree stays put.
		// Checkpointing is best effort: an edit outside the repository, a path
		// the snapshot refuses, or an unreadable file must never abort the run
		// and cut the assistant mid-task.
		logger.Infof("Creating checkpoint pre-edit")
		if err := a.cp.Create("pre-edit", asString(toolParams["file_path"])); err != nil {
			logger.Errorf("Skipping checkpoint for %s: %v", toolName, err)
		}
	}

	logger.Infof("Calling Tool Execute: tool=%s", toolName)
	a.noteToolExecuted()
	result := tool.Execute(tools.WithExecutor(ctx, a.executeDelegatedTool), toolParams)
	if toolName == "Write" || toolName == "Edit" {
		var checkpointErr error
		if result.IsError {
			checkpointErr = a.cp.Discard()
		} else {
			checkpointErr = a.cp.Complete()
		}
		if checkpointErr != nil {
			// The file was already written or the edit was already reported;
			// failing the run now would only hide that outcome from the user.
			logger.Errorf("Failed to finish file checkpoint for %s: %v", toolName, checkpointErr)
			if err := a.cp.Discard(); err != nil {
				logger.Errorf("Failed to discard unfinished checkpoint for %s: %v", toolName, err)
			}
		}
	}
	logger.Infof("Tool %s Execute completed. is_error=%t, output_len=%d", toolName, result.IsError, len(result.Output))
	if result.IsError {
		logger.Errorf("Tool %s execution returned error output: %q", toolName, result.Output)
		a.mu.RLock()
		decider := a.decider
		a.mu.RUnlock()
		if decider != nil && decider.Enabled() && len(strings.TrimSpace(result.Output)) > 0 {
			if diag, err := decider.ClassifyFailure(ctx, result.Output); err == nil && diag != "" {
				logger.Infof("Tool %s failure classified by the decision model: %s", toolName, diag)
				if result.HintsForModel != "" {
					result.HintsForModel += "\n"
				}
				result.HintsForModel += fmt.Sprintf("[Failure Diagnosis: %s]", diag)
			}
		}
	}
	if result.Diff != "" {
		if toolParams == nil {
			toolParams = map[string]any{}
		}
		// Diff is for the human UI only; storing it in params keeps the render
		// path simple without sending the diff back to the model.
		toolParams["_diff"] = result.Diff
	}
	a.paths.RememberToolResult(toolName, toolParams, result.Output, result.IsError)
	if toolName == "TodoWrite" {
		a.maybeShowTodos(toolName)
	} else {
		a.ui.ShowToolCall(toolName, toolParams)
		a.ui.ShowToolResult(toolName, result.Output, result.IsError, toolParams)
		a.maybeShowTodos(toolName)
	}
	if !mode.skipSessionRecord {
		a.recordToolObservation(ctx, toolName, result.Output, result.HintsForModel, mode.structured)
	}

	queueOrRun := func(fn func()) {
		if mode.auxiliaryNotes != nil {
			*mode.auxiliaryNotes = append(*mode.auxiliaryNotes, fn)
		} else {
			fn()
		}
	}

	if !mode.skipSessionRecord && !result.IsError {
		if note := fileEditCompletionNote(toolName, toolParams); note != "" {
			queueOrRun(func() {
				a.sess.AddSystemNote(note)
			})
		}
	}
	if !result.IsError && (toolName == "Write" || toolName == "Edit") {
		if w := a.getWatcher(); w != nil {
			w.RefreshSnapshot()
		}
		if !a.InPlanMode() {
			edited := asString(toolParams["file_path"])
			// Diagnostics first: a language server answers in milliseconds, and a
			// fresh error is a verdict the test suite cannot improve on, so it
			// skips the suite for this edit instead of paying a minute for the
			// same answer.
			postCheck := func() {
				if !a.observeEditDiagnostics(ctx, edited, mode.skipSessionRecord) {
					approve := func(args []string) bool {
						quoted := make([]string, len(args))
						for i, arg := range args {
							quoted[i] = fmt.Sprintf("%q", arg)
						}
						return a.perm.Check("Bash", map[string]any{"command": strings.Join(quoted, " "), "workdir": a.cfg.Cwd}, a.ui)
					}
					if auto := a.autoTest.RunAfterEdit(ctx, edited, approve); strings.TrimSpace(auto) != "" {
						a.ui.ShowToolResult("AUTO-TEST", auto, true, nil)
						if !mode.skipSessionRecord {
							a.recordToolObservation(ctx, "AUTO-TEST", auto, "", false)
						}
					}
				}
			}
			queueOrRun(postCheck)
		}
	}
	if _, loop := a.noProgress.observe(toolCallSignature(toolName, toolParams), toolOutputSignature(result)); loop {
		return result, true, fmt.Errorf("%w: %s produced identical output %d times in a row", ErrNoProgress, toolName, noProgressLimit)
	}
	return result, true, nil
}
func (a *Agent) rescuePathParam(ctx context.Context, toolName string, params map[string]any) {
	key := pathParamKeyForTool(toolName)
	if key == "" {
		return
	}
	raw, ok := params[key].(string)
	if toolName == "DescribeImage" && (!ok || strings.TrimSpace(raw) == "" || strings.Contains(raw, pastedImagePlaceholder)) {
		if img := a.newestImage(); img != "" {
			params[key] = img
			return
		}
	}
	if !ok || strings.TrimSpace(raw) == "" {
		return
	}
	if (toolName == "DescribeImage" || toolName == "Read") && isPastedImageName(raw) {
		// The model is chasing a pasted image by name. The newest attached
		// image is what the user means; older names are stale history.
		if img := a.newestImage(); img != "" {
			if img != raw {
				params[key] = img
				a.ui.ShowToolResult(toolName, fmt.Sprintf("using the newest pasted image instead of %q", raw), false, nil)
			}
			return
		}
	}
	if abs, rescued, ok := a.paths.Resolve(raw); ok {
		if abs != raw {
			params[key] = abs
			if rescued {
				a.ui.ShowToolResult(toolName, fmt.Sprintf("rescued path %q → %s", raw, abs), false, nil)
			}
		}
		return
	}
	// A DescribeImage call on a path that resolves to nothing almost always
	// means "the image the user just sent"; look at that one.
	if toolName == "DescribeImage" {
		if img := a.newestImage(); img != "" {
			params[key] = img
			a.ui.ShowToolResult(toolName, fmt.Sprintf("using the newest pasted image instead of %q", raw), false, nil)
			return
		}
	}
	// A Write target usually does not exist yet, so Resolve declines it; still
	// anchor a relative path to the working directory instead of rejecting it
	// outright (models routinely write "report.md" or "src/new.go").
	if toolName == "Write" {
		if abs, rescued := a.paths.ResolveTarget(raw); abs != "" {
			params[key] = abs
			if rescued {
				a.ui.ShowToolResult(toolName, fmt.Sprintf("rescued path %q → %s", raw, abs), false, nil)
			}
			return
		}
	}
	// Resolve declined: try sidecar disambiguation across remembered
	// candidates. This only kicks in when there are 2+ matches under the
	// same basename, which is exactly the case where the deterministic
	// rescuer is blind on purpose.
	a.mu.RLock()
	side := a.side
	decider := a.decider
	goal := a.currentGoal
	a.mu.RUnlock()

	cands := a.paths.Candidates(raw)
	if len(cands) < 2 {
		return
	}
	hint := fmt.Sprintf("model wrote %q while user goal was: %s", raw, goal)

	if decider != nil && decider.Enabled() {
		a.ui.StartWaiting(fmt.Sprintf("disambiguating %q via %s…",
			raw, shortModelName(a.cfg.DecisionModel)))
		chosen, ok, err := decider.DisambiguatePath(ctx, hint, cands)
		a.ui.StopWaiting()
		if err == nil && ok && chosen != "" {
			params[key] = chosen
			a.ui.ShowToolResult(toolName, fmt.Sprintf("decision model disambiguated %q → %s", raw, chosen), false, nil)
			return
		}
	}

	if side == nil || !side.Enabled() {
		return
	}
	// Same rationale as recordToolObservation: sidecar calls are slow
	// enough on cold-start that an unannounced 5–20s pause feels like a
	// hang. A short waiting label tells the user a small model is being
	// queried and why.
	a.ui.StartWaiting(fmt.Sprintf("disambiguating %q via %s…",
		raw, shortModelName(a.cfg.SidecarModel)))
	chosen, ok, err := side.DisambiguatePath(ctx, hint, cands)
	a.ui.StopWaiting()
	if err != nil || !ok {
		return
	}
	params[key] = chosen
	a.ui.ShowToolResult(toolName, fmt.Sprintf("sidecar disambiguated %q → %s", raw, chosen), false, nil)
}
func (a *Agent) maybeShowTodos(toolName string) {
	if toolName != "TodoWrite" {
		return
	}
	tw := a.todoWriteTool()
	if tw == nil {
		return
	}
	snap := tw.Store().Snapshot()
	if len(snap) == 0 {
		return
	}
	items := make([]tui.TodoItem, 0, len(snap))
	for _, it := range snap {
		if !isMeaningfulTodoContent(it.Content) {
			continue
		}
		items = append(items, tui.TodoItem{
			ID:      it.ID,
			Content: it.Content,
			Status:  it.Status,
		})
	}
	if len(items) == 0 {
		return
	}
	a.ui.ShowTodos(items)
}
func fileEditCompletionNote(toolName string, params map[string]any) string {
	if toolName != "Write" && toolName != "Edit" {
		return ""
	}
	path := strings.TrimSpace(asString(params["file_path"]))
	if path == "" {
		return ""
	}
	verb := "updated"
	if toolName == "Write" {
		verb = "written"
	}
	return fmt.Sprintf("File %s: %s.", verb, path)
}
func (a *Agent) recordToolObservation(ctx context.Context, toolName, output, hintsForModel string, structured bool) {
	a.mu.RLock()
	side := a.side
	a.mu.RUnlock()
	obs := output
	if hintsForModel != "" {
		obs = output + "\n\n[assistant-hints]\n" + hintsForModel + "\n[/assistant-hints]"
	}
	record := func(name, output string) {
		if structured {
			a.sess.AddToolResult(name, output)
			return
		}
		a.sess.AddToolObservation(name, output)
	}
	// Use the Pool's configured threshold (not the package default) so
	// tests that lower it via WithSummariseThreshold still exercise the
	// summary path and so users that raise it via env don't pay for a
	// useless spinner on outputs the sidecar would skip anyway.
	if side != nil && side.Enabled() && toolName != "Read" && len(obs) >= side.Threshold() {
		label := fmt.Sprintf("condensing %s output via %s…",
			toolName, shortModelName(a.cfg.SidecarModel))
		a.ui.StartWaiting(label)
		summary, used, _ := side.SummariseToolOutput(ctx, toolName, obs)
		a.ui.StopWaiting()
		if used && summary != "" {
			record(toolName, summary)
			a.ui.ShowToolResult(toolName,
				fmt.Sprintf("sidecar condensed %d bytes → summary stored in context", len(obs)),
				false, nil)
			return
		}
	}
	record(toolName, obs)
}
