package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/background"
)

// BackgroundRunTool starts a shell command asynchronously in the background.
type BackgroundRunTool struct {
	mgr *background.Manager
}

func NewBackgroundRunTool() *BackgroundRunTool {
	return &BackgroundRunTool{mgr: background.DefaultManager()}
}

func (t *BackgroundRunTool) Name() string { return "BackgroundRun" }
func (t *BackgroundRunTool) Description() string {
	return "Run a shell command asynchronously in the background. Use this for commands that take time (such as downloads, long builds, background scripts, or servers). Returns immediately with a task_id and status while execution continues in the background."
}

func (t *BackgroundRunTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The shell command to run in the background",
					},
					"workdir": map[string]any{
						"type":        "string",
						"description": "Optional working directory for the command",
					},
					"initial_wait_ms": map[string]any{
						"type":        "integer",
						"description": "Time in ms to wait before returning (default 500, max 10000). If the command finishes within this window, the final result is returned immediately; otherwise confirms the task is running in the background.",
					},
				},
				"required": []string{"command"},
			},
		},
	}
}

func (t *BackgroundRunTool) Execute(ctx context.Context, params map[string]any) Result {
	command, ok := params["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return errResult("command is required")
	}

	workdir, _ := params["workdir"].(string)

	initialWaitMS := int64(500)
	switch v := params["initial_wait_ms"].(type) {
	case float64:
		initialWaitMS = int64(v)
	case int:
		initialWaitMS = int64(v)
	case string:
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			initialWaitMS = parsed
		}
	}
	if initialWaitMS < 0 {
		initialWaitMS = 0
	}
	if initialWaitMS > 10000 {
		initialWaitMS = 10000
	}

	task, err := t.mgr.Start(command, workdir, time.Duration(initialWaitMS)*time.Millisecond)
	if err != nil {
		return errResult(fmt.Sprintf("failed to start background task: %v", err))
	}

	if task.IsRunning() {
		output := stripANSI(task.TailOutput(30))
		if output == "" {
			output = "(no initial output yet)"
		}
		res := fmt.Sprintf(
			"[task_id: %s]\n"+
				"[status: STILL RUNNING - execution in background]\n"+
				"[pid: %d]\n"+
				"[elapsed: %s]\n"+
				"[note: The command is currently running in the background and has NOT finished yet. Use BackgroundStatus to check progress or wait for it, or BackgroundKill to stop it.]\n\n"+
				"Initial output:\n%s",
			task.ID, task.PID, task.Elapsed().Round(100*time.Millisecond), strings.TrimRight(output, "\n"),
		)
		return Result{Output: res}
	}

	// Finished during initial wait
	snap := task.Snapshot()
	output := stripANSI(task.Output())
	if output == "" {
		output = "(no output)"
	}
	statusStr := "COMPLETED"
	if snap.Status == background.StatusFailed {
		statusStr = "FAILED"
	}
	res := fmt.Sprintf(
		"[task_id: %s]\n"+
			"[status: %s]\n"+
			"[exit_code: %d]\n"+
			"[elapsed: %s]\n\n"+
			"Output:\n%s",
		snap.ID, statusStr, snap.ExitCode, snap.Elapsed, strings.TrimRight(output, "\n"),
	)
	return Result{Output: res, IsError: snap.ExitCode != 0}
}

// BackgroundStatusTool checks status and output of a background task.
type BackgroundStatusTool struct {
	mgr *background.Manager
}

func NewBackgroundStatusTool() *BackgroundStatusTool {
	return &BackgroundStatusTool{mgr: background.DefaultManager()}
}

func (t *BackgroundStatusTool) Name() string { return "BackgroundStatus" }
func (t *BackgroundStatusTool) Description() string {
	return "Check the current execution status and output of a background task. Set wait_ms > 0 to wait (poll) up to that duration for completion or new output. Allows inspecting progress whenever you want or waiting for completion."
}

func (t *BackgroundStatusTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "The background task ID (e.g. 'bg-1')",
					},
					"wait_ms": map[string]any{
						"type":        "integer",
						"description": "Milliseconds to wait for completion or new output (default 0). Set > 0 (e.g. 5000) to wait up to that duration.",
					},
					"tail_lines": map[string]any{
						"type":        "integer",
						"description": "Number of recent output lines to return (default 50)",
					},
				},
				"required": []string{"task_id"},
			},
		},
	}
}

func (t *BackgroundStatusTool) Execute(ctx context.Context, params map[string]any) Result {
	taskID, ok := params["task_id"].(string)
	if !ok || strings.TrimSpace(taskID) == "" {
		return errResult("task_id is required")
	}

	waitMS := int64(0)
	switch v := params["wait_ms"].(type) {
	case float64:
		waitMS = int64(v)
	case int:
		waitMS = int64(v)
	case string:
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			waitMS = parsed
		}
	}
	if waitMS < 0 {
		waitMS = 0
	}
	if waitMS > 600000 {
		waitMS = 600000
	}

	tailLines := 50
	switch v := params["tail_lines"].(type) {
	case float64:
		tailLines = int(v)
	case int:
		tailLines = v
	}
	if tailLines <= 0 {
		tailLines = 50
	}

	task, _, err := t.mgr.Wait(ctx, taskID, time.Duration(waitMS)*time.Millisecond)
	if err != nil && task == nil {
		return errResult(err.Error())
	}

	snap := task.Snapshot()
	if snap.Status == background.StatusRunning {
		output := stripANSI(task.TailOutput(tailLines))
		if output == "" {
			output = "(no output recorded yet)"
		}
		res := fmt.Sprintf(
			"[task_id: %s]\n"+
				"[status: STILL RUNNING - execution has NOT finished yet]\n"+
				"[pid: %d]\n"+
				"[elapsed: %s]\n"+
				"[note: The task is currently running in the background. You can check again later or wait with wait_ms.]\n\n"+
				"Recent output:\n%s",
			snap.ID, snap.PID, snap.Elapsed, strings.TrimRight(output, "\n"),
		)
		return Result{Output: res}
	}

	output := stripANSI(task.TailOutput(tailLines))
	if output == "" {
		output = "(no output)"
	}

	switch snap.Status {
	case background.StatusCompleted:
		res := fmt.Sprintf(
			"[task_id: %s]\n"+
				"[status: COMPLETED]\n"+
				"[exit_code: %d]\n"+
				"[total_duration: %s]\n\n"+
				"Output:\n%s",
			snap.ID, snap.ExitCode, snap.Elapsed, strings.TrimRight(output, "\n"),
		)
		return Result{Output: res}
	case background.StatusFailed:
		res := fmt.Sprintf(
			"[task_id: %s]\n"+
				"[status: FAILED]\n"+
				"[exit_code: %d]\n"+
				"[total_duration: %s]\n\n"+
				"Output:\n%s",
			snap.ID, snap.ExitCode, snap.Elapsed, strings.TrimRight(output, "\n"),
		)
		return Result{Output: res, IsError: true}
	case background.StatusKilled:
		res := fmt.Sprintf(
			"[task_id: %s]\n"+
				"[status: KILLED]\n"+
				"[total_duration: %s]\n\n"+
				"Output:\n%s",
			snap.ID, snap.Elapsed, strings.TrimRight(output, "\n"),
		)
		return Result{Output: res}
	default:
		return Result{Output: fmt.Sprintf("[task_id: %s] [status: %s]", snap.ID, snap.Status)}
	}
}

// BackgroundListTool lists all background tasks.
type BackgroundListTool struct {
	mgr *background.Manager
}

func NewBackgroundListTool() *BackgroundListTool {
	return &BackgroundListTool{mgr: background.DefaultManager()}
}

func (t *BackgroundListTool) Name() string { return "BackgroundList" }
func (t *BackgroundListTool) Description() string {
	return "List all background tasks, including their ID, command, status (running, completed, failed, killed), and elapsed time."
}

func (t *BackgroundListTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{
						"type":        "string",
						"description": "Optional filter by status ('running', 'completed', 'failed', 'killed', 'all')",
					},
				},
			},
		},
	}
}

func (t *BackgroundListTool) Execute(_ context.Context, params map[string]any) Result {
	filterStatus := ""
	if s, ok := params["status"].(string); ok {
		filterStatus = strings.ToLower(strings.TrimSpace(s))
	}

	tasks := t.mgr.List()
	if len(tasks) == 0 {
		return Result{Output: "No background tasks found."}
	}

	var sb strings.Builder
	count := 0
	for _, task := range tasks {
		snap := task.Snapshot()
		if filterStatus != "" && filterStatus != "all" && string(snap.Status) != filterStatus {
			continue
		}
		count++
		statusDesc := string(snap.Status)
		if snap.Status == background.StatusRunning {
			statusDesc = "RUNNING (has NOT finished yet)"
		} else if snap.Status == background.StatusCompleted {
			statusDesc = fmt.Sprintf("COMPLETED (exit 0)")
		} else if snap.Status == background.StatusFailed {
			statusDesc = fmt.Sprintf("FAILED (exit %d)", snap.ExitCode)
		}

		sb.WriteString(fmt.Sprintf("- %s: [%s] command: %q, elapsed: %s, pid: %d\n",
			snap.ID, statusDesc, snap.Command, snap.Elapsed, snap.PID))
	}

	if count == 0 {
		return Result{Output: fmt.Sprintf("No background tasks matching status %q.", filterStatus)}
	}

	return Result{Output: strings.TrimRight(sb.String(), "\n")}
}

// BackgroundKillTool terminates a background task.
type BackgroundKillTool struct {
	mgr *background.Manager
}

func NewBackgroundKillTool() *BackgroundKillTool {
	return &BackgroundKillTool{mgr: background.DefaultManager()}
}

func (t *BackgroundKillTool) Name() string { return "BackgroundKill" }
func (t *BackgroundKillTool) Description() string {
	return "Terminate or cancel a running background task by its task_id."
}

func (t *BackgroundKillTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "The background task ID to terminate (e.g. 'bg-1')",
					},
				},
				"required": []string{"task_id"},
			},
		},
	}
}

func (t *BackgroundKillTool) Execute(_ context.Context, params map[string]any) Result {
	taskID, ok := params["task_id"].(string)
	if !ok || strings.TrimSpace(taskID) == "" {
		return errResult("task_id is required")
	}

	task := t.mgr.Get(taskID)
	if task == nil {
		return errResult(fmt.Sprintf("background task not found: %s", taskID))
	}

	snap := task.Snapshot()
	if snap.Status != background.StatusRunning {
		return Result{Output: fmt.Sprintf("Task %s is not running (status: %s).", taskID, snap.Status)}
	}

	if err := t.mgr.Kill(taskID); err != nil {
		return errResult(fmt.Sprintf("failed to kill task %s: %v", taskID, err))
	}

	return Result{Output: fmt.Sprintf("Task %s terminated successfully. Status: KILLED.", taskID)}
}
