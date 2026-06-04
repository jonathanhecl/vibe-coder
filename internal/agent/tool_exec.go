package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func (a *Agent) executeTool(ctx context.Context, tool tools.Tool, toolName string, toolParams map[string]any, mode toolExecutionMode) (tools.Result, bool, error) {
	if a.InPlanMode() && toolName == "Write" && !a.isWriteAllowedInPlan(toolParams) {
		blockMsg := "Write blocked in plan mode. Allowed path: <cwd>/.vibe-coder/plans/"
		a.ui.ShowToolResult(toolName, blockMsg, true, toolParams)
		a.sess.AddSystemNote(blockMsg)
		return tools.Result{}, false, nil
	}

	a.rescuePathParam(ctx, toolName, toolParams)
	if !a.perm.Check(toolName, toolParams, a.ui) {
		deny := permissionDeniedNote(a.perm)
		if mode.showPermissionDeniedResult {
			a.ui.ShowToolResult(toolName, deny, true, toolParams)
		}
		a.sess.AddSystemNote(deny)
		if mode.endAssistantOnDenied {
			a.ui.EndAssistant()
		}
		return tools.Result{}, false, nil
	}

	if toolName == "Write" || toolName == "Edit" {
		// Create a checkpoint before mutating files so failed edits can be
		// inspected or rolled back by the user outside the agent loop.
		if err := a.cp.Create("pre-edit"); err != nil {
			return tools.Result{}, false, err
		}
	}

	result := tool.Execute(ctx, toolParams)
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
	a.recordToolObservation(ctx, toolName, result.Output, result.HintsForModel)
	if !result.IsError {
		if note := fileEditCompletionNote(toolName, toolParams); note != "" {
			a.sess.AddSystemNote(note)
		}
	}
	if !result.IsError && (toolName == "Write" || toolName == "Edit") {
		if w := a.getWatcher(); w != nil {
			w.RefreshSnapshot()
		}
		if auto := a.autoTest.RunAfterEdit(ctx, asString(toolParams["file_path"])); strings.TrimSpace(auto) != "" {
			a.ui.ShowToolResult("AUTO-TEST", auto, true, nil)
			a.recordToolObservation(ctx, "AUTO-TEST", auto, "")
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
	return fmt.Sprintf("File %s: %s. Treat this step as completed; do not recreate or re-edit this file unless verification shows a real mismatch.", verb, path)
}
func (a *Agent) recordToolObservation(ctx context.Context, toolName, output, hintsForModel string) {
	a.mu.RLock()
	side := a.side
	a.mu.RUnlock()
	obs := output
	if hintsForModel != "" {
		obs = output + "\n\n[assistant-hints]\n" + hintsForModel + "\n[/assistant-hints]"
	}
	// Use the Pool's configured threshold (not the package default) so
	// tests that lower it via WithSummariseThreshold still exercise the
	// summary path and so users that raise it via env don't pay for a
	// useless spinner on outputs the sidecar would skip anyway.
	if side != nil && side.Enabled() && len(obs) >= side.Threshold() {
		label := fmt.Sprintf("condensing %s output via %s…",
			toolName, shortModelName(a.cfg.SidecarModel))
		a.ui.StartWaiting(label)
		summary, used, _ := side.SummariseToolOutput(ctx, toolName, obs)
		a.ui.StopWaiting()
		if used && summary != "" {
			a.sess.AddToolObservation(toolName, summary)
			a.ui.ShowToolResult(toolName,
				fmt.Sprintf("sidecar condensed %d bytes → summary stored in context", len(obs)),
				false, nil)
			return
		}
	}
	a.sess.AddToolObservation(toolName, obs)
}
func (a *Agent) isWriteAllowedInPlan(params map[string]any) bool {
	rawPath, _ := params["file_path"].(string)
	if strings.TrimSpace(rawPath) == "" {
		return false
	}
	absPath := rawPath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(a.cfg.Cwd, absPath)
	}
	resolvedPath := absPath
	if v, err := filepath.EvalSymlinks(absPath); err == nil && strings.TrimSpace(v) != "" {
		resolvedPath = v
	}

	allowedRoot := filepath.Join(a.cfg.Cwd, ".vibe-coder", "plans")
	_ = os.MkdirAll(allowedRoot, 0o755)
	resolvedRoot := allowedRoot
	if v, err := filepath.EvalSymlinks(allowedRoot); err == nil && strings.TrimSpace(v) != "" {
		resolvedRoot = v
	}

	pathAbs, err := filepath.Abs(resolvedPath)
	if err != nil {
		return false
	}
	rootAbs, err := filepath.Abs(resolvedRoot)
	if err != nil {
		return false
	}
	if pathAbs == rootAbs {
		return true
	}
	return strings.HasPrefix(pathAbs, rootAbs+string(filepath.Separator))
}
