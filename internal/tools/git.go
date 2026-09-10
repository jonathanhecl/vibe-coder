package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	gitx "github.com/jonathanhecl/vibe-coder/internal/git"
)

const (
	gitDiffMaxBytes   = 20 * 1024
	gitStatusMaxBytes = 8 * 1024
)

type GitStatusTool struct{}

func NewGitStatusTool() *GitStatusTool { return &GitStatusTool{} }

func (t *GitStatusTool) Name() string { return "GitStatus" }
func (t *GitStatusTool) Description() string {
	return "Show git working-tree status (branch + short status)."
}
func (t *GitStatusTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (t *GitStatusTool) Execute(ctx context.Context, _ map[string]any) Result {
	cwd := gitToolCwd()
	out, err := runGit(ctx, cwd, "status", "--short", "--branch")
	if err != nil {
		return errResult(gitFriendlyError(out, err))
	}
	trimmed := strings.TrimSpace(out)
	// With --branch, a clean tree still prints the "## branch" header.
	// Only non-header lines mean real changes.
	dirty := false
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "##") {
			continue
		}
		dirty = true
		break
	}
	if !dirty {
		return Result{Output: "(clean)"}
	}
	return Result{Output: truncateMiddle(trimmed, gitStatusMaxBytes)}
}

type GitDiffTool struct{}

func NewGitDiffTool() *GitDiffTool { return &GitDiffTool{} }

func (t *GitDiffTool) Name() string { return "GitDiff" }
func (t *GitDiffTool) Description() string {
	return "Show git diff for the working tree (optional file_path for one file)."
}
func (t *GitDiffTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string"},
				},
			},
		},
	}
}

func (t *GitDiffTool) Execute(ctx context.Context, params map[string]any) Result {
	cwd := gitToolCwd()
	if path, _ := params["file_path"].(string); strings.TrimSpace(path) != "" {
		out, err := runGit(ctx, cwd, "diff", "--", strings.TrimSpace(path))
		if err != nil {
			return errResult(gitFriendlyError(out, err))
		}
		if strings.TrimSpace(out) == "" {
			return Result{Output: "(no diff)"}
		}
		return Result{Output: truncateMiddle(out, gitDiffMaxBytes)}
	}
	stat, err := runGit(ctx, cwd, "diff", "--stat")
	if err != nil {
		return errResult(gitFriendlyError(stat, err))
	}
	if strings.TrimSpace(stat) == "" {
		return Result{Output: "(no diff)"}
	}
	full, err := runGit(ctx, cwd, "diff")
	if err != nil {
		return Result{Output: truncateMiddle(stat, gitDiffMaxBytes)}
	}
	combined := strings.TrimRight(stat, "\n") + "\n\n" + strings.TrimSpace(full)
	return Result{Output: truncateMiddle(combined, gitDiffMaxBytes)}
}

type GitUndoTool struct{}

func NewGitUndoTool() *GitUndoTool { return &GitUndoTool{} }

func (t *GitUndoTool) Name() string { return "GitUndo" }
func (t *GitUndoTool) Description() string {
	return "Restore the latest vibe-coder file checkpoint (git stash pop). Use after a bad Write/Edit."
}
func (t *GitUndoTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (t *GitUndoTool) Execute(ctx context.Context, _ map[string]any) Result {
	cwd := gitToolCwd()
	cp := gitx.NewCheckpoint(cwd)
	if !cp.IsRepo() {
		return errResult("Not a git repository.")
	}
	listStash, err := runGit(ctx, cwd, "stash", "list")
	if err != nil {
		return errResult(gitFriendlyError(listStash, err))
	}
	if !strings.Contains(listStash, "vibe-coder/") {
		return Result{Output: "No vibe-coder checkpoint found (nothing to undo)."}
	}
	if err := cp.Rollback(); err != nil {
		return errResult(fmt.Sprintf("restore checkpoint: %v", err))
	}
	status, _ := runGit(ctx, cwd, "status", "--short", "--branch")
	note := "Restored the latest vibe-coder checkpoint (git stash pop)."
	if trimmed := strings.TrimSpace(status); trimmed != "" {
		note += "\n\n" + truncateMiddle(trimmed, gitStatusMaxBytes)
	}
	return Result{Output: note}
}

func gitToolCwd() string {
	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		return cwd
	}
	return "."
}

func runGit(ctx context.Context, cwd string, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func gitFriendlyError(out string, err error) string {
	if strings.Contains(strings.ToLower(out+err.Error()), "not a git repository") {
		return "Not a git repository."
	}
	combined := strings.TrimSpace(out)
	if combined == "" {
		return fmt.Sprintf("git failed: %v", err)
	}
	return fmt.Sprintf("git failed: %v\n%s", err, combined)
}

func truncateMiddle(s string, maxBytes int) string {
	s = strings.TrimRight(s, "\n")
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	head := maxBytes / 2
	tail := maxBytes - head
	return s[:head] + "\n... (truncated) ...\n" + s[len(s)-tail:]
}
