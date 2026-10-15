package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/background"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func runTasksCommand(c *Ctx, args []string) error {
	mgr := background.DefaultManager()
	st := tui.NewStyle(c.Out)

	if len(args) > 0 {
		sub := strings.ToLower(args[0])
		if sub == "kill" || sub == "cancel" || sub == "stop" {
			if len(args) < 2 {
				fmt.Fprintln(c.Out, "Usage: /tasks kill <task_id>")
				return nil
			}
			taskID := args[1]
			task := mgr.Get(taskID)
			if task == nil {
				fmt.Fprintf(c.Out, "Background task %q not found.\n", taskID)
				return nil
			}
			if !task.IsRunning() {
				fmt.Fprintf(c.Out, "Background task %s is not running (status: %s).\n", taskID, task.Status)
				return nil
			}
			if err := mgr.Kill(taskID); err != nil {
				fmt.Fprintf(c.Out, "Failed to kill background task %s: %v\n", taskID, err)
				return nil
			}
			fmt.Fprintf(c.Out, "Background task %s terminated.\n", taskID)
			return nil
		}
	}

	tasks := mgr.List()
	if len(tasks) == 0 {
		fmt.Fprintln(c.Out, "No background tasks found.")
		return nil
	}

	fmt.Fprintln(c.Out, st.BoldCyan("Background tasks:"))
	for _, t := range tasks {
		snap := t.Snapshot()
		var statusBadge string
		switch snap.Status {
		case background.StatusRunning:
			statusBadge = st.BoldYellow("RUNNING (in background)")
		case background.StatusCompleted:
			statusBadge = st.BrightGreen("COMPLETED")
		case background.StatusFailed:
			statusBadge = st.BoldRed(fmt.Sprintf("FAILED (exit %d)", snap.ExitCode))
		case background.StatusKilled:
			statusBadge = st.Dim("KILLED")
		default:
			statusBadge = string(snap.Status)
		}

		fmt.Fprintf(c.Out, "  %s  [%s]  %s (pid: %d, elapsed: %s)\n",
			st.Bold(snap.ID), statusBadge, snap.Command, snap.PID, snap.Elapsed)
	}
	fmt.Fprintln(c.Out, st.Dim("Use '/tasks kill <id>' to stop a running task."))
	return nil
}
