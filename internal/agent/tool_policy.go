package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func (a *Agent) toolModeBlock(tool tools.Tool, params map[string]any) string {
	effect := tools.EffectOf(tool)
	safe := effect == tools.EffectRead || effect == tools.EffectState || effect == tools.EffectDelegate
	if a.InReviewMode() && !safe {
		return fmt.Sprintf("%s blocked in review mode. Only read-only tools are allowed.", tool.Name())
	}
	if a.InPlanMode() && !safe {
		switch tool.(type) {
		case *tools.WriteTool, *tools.EditTool:
			if a.isWriteAllowedInPlan(params) {
				return ""
			}
		}
		return fmt.Sprintf("%s blocked in plan mode. Only read-only tools and Write/Edit inside <cwd>/.vibe-coder/plans/ are allowed.", tool.Name())
	}
	return ""
}

func (a *Agent) executeDelegatedTool(ctx context.Context, requested tools.Tool, params map[string]any) (tools.Result, error) {
	a.delegatedMu.Lock()
	defer a.delegatedMu.Unlock()
	tool := a.reg.Get(requested.Name())
	if tool == nil {
		return tools.Result{}, fmt.Errorf("delegated tool is unavailable: %s", requested.Name())
	}
	result, executed, err := a.executeTool(ctx, tool, tool.Name(), params, toolExecutionMode{showPermissionDeniedResult: true})
	if err != nil {
		return result, err
	}
	if !executed {
		return result, fmt.Errorf("delegated tool blocked: %s", result.Output)
	}
	return result, nil
}

func (a *Agent) isWriteAllowedInPlan(params map[string]any) bool {
	rawPath := strings.TrimSpace(asString(params["file_path"]))
	if rawPath == "" {
		return false
	}
	cwd, err := filepath.Abs(a.cfg.Cwd)
	if err != nil {
		return false
	}
	path := rawPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	allowedRoot := filepath.Join(cwd, ".vibe-coder", "plans")
	rel, err := filepath.Rel(allowedRoot, path)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return false
	}
	fromCwd, err := filepath.Rel(cwd, path)
	if err != nil || !filepath.IsLocal(fromCwd) {
		return false
	}
	ancestor := cwd
	for _, part := range strings.Split(fromCwd, string(filepath.Separator)) {
		ancestor = filepath.Join(ancestor, part)
		info, err := os.Lstat(ancestor)
		if err != nil && !os.IsNotExist(err) {
			return false
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}
