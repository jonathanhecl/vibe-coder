package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// GoalTaskFromSlash extracts the goal string from "/goal <objective>" or
// "/mission start <objective>".
func GoalTaskFromSlash(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return "", false
	}
	if strings.EqualFold(fields[0], "/goal") {
		if len(fields) > 1 {
			task := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))
			return task, task != ""
		}
		return "", false
	}
	if len(fields) >= 2 && strings.EqualFold(fields[0], "/mission") && strings.EqualFold(fields[1], "start") {
		if len(fields) > 2 {
			task := strings.TrimSpace(strings.Join(fields[2:], " "))
			return task, task != ""
		}
		return "", false
	}
	return "", false
}

// FormatGoalPrompt constructs the direct autonomous goal prompt instructing the
// agent to execute concrete steps right away without verification theater.
func FormatGoalPrompt(goal string) string {
	return fmt.Sprintf(`[goal] %s

Work autonomously until this goal is complete:
- Use TodoWrite only if the task needs 3+ distinct steps, and start executing the first concrete step immediately.
- Inspect only what you need, then edit or run commands right away. Do not re-read unchanged files or stack redundant checks.
- If a genuine requirement is ambiguous, call AskUserQuestion once.
- When finished, mark all TODO items completed and reply with a concise summary.`, goal)
}

// runGoalCommand handles the /goal slash command.
func runGoalCommand(c *Ctx, args []string) error {
	mc, ok := c.Agent.(missionController)
	if !ok {
		fmt.Fprintln(c.Out, "Goal and mission control is unavailable in this session.")
		return nil
	}
	goal := strings.TrimSpace(strings.Join(args, " "))
	if goal == "" {
		m := mc.MissionSnapshot()
		if m.Status == tools.MissionStatusActive {
			printMissionStatus(c, m)
			return nil
		}
		fmt.Fprintln(c.Out, "Usage: /goal <objective>")
		fmt.Fprintln(c.Out, "Start an autonomous goal. The agent executes the checklist with unattended permissions")
		fmt.Fprintln(c.Out, "and stops automatically when the work is finished.")
		return nil
	}

	m := mc.MissionSnapshot()
	if m.Status == tools.MissionStatusActive {
		st := tui.NewStyle(c.Out)
		fmt.Fprintf(c.Out, "%s (current goal: %q).\n",
			st.Yellow("A mission is already active"),
			m.Goal,
		)
		fmt.Fprintln(c.Out, "End it with /mission done or cancel it with /mission cancel before starting a new goal.")
		return nil
	}

	mc.StartMission(goal)
	st := tui.NewStyle(c.Out)
	fmt.Fprintf(c.Out, "%s %s\n", st.BoldCyan("[goal] Autonomous mission initialized:"), st.BrightWhite(goal))
	fmt.Fprintln(c.Out, st.Dim("Working autonomously until complete (unattended permissions)..."))
	return nil
}
