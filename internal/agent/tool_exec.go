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
		a.sess.AddSystemNote(blockMsg)
		return tools.Result{Output: blockMsg, IsError: true}, false, nil
	}
	if !a.perm.Check(toolName, toolParams, a.ui) {
		deny := permissionDeniedNote(a.perm)
		logger.Errorf("Tool %s execution denied by permissions", toolName)
		if mode.showPermissionDeniedResult {
			a.ui.ShowToolResult(toolName, deny, true, toolParams)
		}
		a.sess.AddSystemNote(deny)
		if mode.endAssistantOnDenied {
			a.ui.EndAssistant()
		}
		return tools.Result{Output: deny, IsError: true}, false, nil
	}

	if toolName == "Write" || toolName == "Edit" {
		// Create a checkpoint before mutating files so failed edits can be
		// inspected or rolled back by the user outside the agent loop.
		// The snapshot covers only the edited file; the working tree stays put.
		logger.Infof("Creating checkpoint pre-edit")
		if err := a.cp.Create("pre-edit", asString(toolParams["file_path"])); err != nil {
			logger.Errorf("Failed to create checkpoint: %v", err)
			return tools.Result{}, false, err
		}
	}

	logger.Infof("Calling Tool Execute: tool=%s", toolName)
	result := tool.Execute(tools.WithExecutor(ctx, a.executeDelegatedTool), toolParams)
	if toolName == "Write" || toolName == "Edit" {
		var checkpointErr error
		if result.IsError {
			checkpointErr = a.cp.Discard()
		} else {
			checkpointErr = a.cp.Complete()
		}
		if checkpointErr != nil {
			return result, true, fmt.Errorf("finish file checkpoint: %w", checkpointErr)
		}
	}
	logger.Infof("Tool %s Execute completed. is_error=%t, output_len=%d", toolName, result.IsError, len(result.Output))
	if result.IsError {
		logger.Errorf("Tool %s execution returned error output: %q", toolName, result.Output)
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
	a.recordToolObservation(ctx, toolName, result.Output, result.HintsForModel, mode.structured)
	if !result.IsError {
		if note := fileEditCompletionNote(toolName, toolParams); note != "" {
			a.sess.AddSystemNote(note)
		}
	}
	if !result.IsError && (toolName == "Write" || toolName == "Edit") {
		if w := a.getWatcher(); w != nil {
			w.RefreshSnapshot()
		}
		if !a.InPlanMode() {
			approve := func(args []string) bool {
				quoted := make([]string, len(args))
				for i, arg := range args {
					quoted[i] = fmt.Sprintf("%q", arg)
				}
				return a.perm.Check("Bash", map[string]any{"command": strings.Join(quoted, " "), "workdir": a.cfg.Cwd}, a.ui)
			}
			if auto := a.autoTest.RunAfterEdit(ctx, asString(toolParams["file_path"]), approve); strings.TrimSpace(auto) != "" {
				a.ui.ShowToolResult("AUTO-TEST", auto, true, nil)
				a.recordToolObservation(ctx, "AUTO-TEST", auto, "", false)
			}
		}
	}
	return result, true, nil
}
func (a *Agent) rescuePathParam(ctx context.Context, toolName string, params map[string]any) {
	key := pathParamKeyForTool(toolName)
	if key == "" {
		return
	}
	raw, ok := params[key].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return
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
	// Resolve declined: try sidecar disambiguation across remembered
	// candidates. This only kicks in when there are 2+ matches under the
	// same basename, which is exactly the case where the deterministic
	// rescuer is blind on purpose.
	a.mu.RLock()
	side := a.side
	goal := a.currentGoal
	a.mu.RUnlock()
	if side == nil || !side.Enabled() {
		return
	}
	cands := a.paths.Candidates(raw)
	if len(cands) < 2 {
		return
	}
	hint := fmt.Sprintf("model wrote %q while user goal was: %s", raw, goal)
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
	return fmt.Sprintf("File %s: %s. Verify the change now: Read the edited region and run the relevant check (tests, build, or lint via Bash). Re-edit only if verification proves it is still wrong; otherwise move on to the next step.", verb, path)
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
