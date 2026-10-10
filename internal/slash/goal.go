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

// FormatGoalPrompt constructs the phased autonomous mission prompt instructing
// the agent to first plan step-by-step and design verification, ask clarifying
// questions if needed, and then execute autonomously without routine user prompts.
func FormatGoalPrompt(goal string) string {
	return fmt.Sprintf(`[goal] Autonomous goal: %q

Phase 1: Planning, Verification Design & Clarification
1. Inspect the workspace and analyze what is needed.
2. Break down the goal into clear, concrete steps using TodoWrite.
3. For EVERY step, specify how you will corroborate and verify that it is correct (e.g. running tests, linters, verifying build, inspecting file content or command output). Verification is mandatory before considering any step complete.
4. If you have critical ambiguities, questions, or need architectural decisions from the user, ask now using AskUserQuestion before proceeding.
5. If requirements are clear, begin executing the first step immediately with tool calls.

Phase 2: Autonomous Execution & Verification
Once the plan is established:
- Work through each step sequentially using tool calls.
- After every edit or change, run the verification step you defined to confirm it works.
- Do not stop for routine user approvals; the runtime is in unattended mode and will keep driving the mission.
- Continue until all checklist items are done and verified, then call MissionComplete with a concise summary.
- If and only if you are completely blocked and cannot proceed without user intervention, call MissionBlocked with the reason.`, goal)
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
		fmt.Fprintln(c.Out, "Start an autonomous mission. The agent will first organize a step-by-step plan")
		fmt.Fprintln(c.Out, "and verification strategy, clarify any doubts with you, and then execute autonomously.")
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
	if c.Session != nil {
		c.Session.AddUser("[System Note] Autonomous mission started: " + goal)
	}
	st := tui.NewStyle(c.Out)
	fmt.Fprintf(c.Out, "%s %s\n", st.BoldCyan("[goal] Autonomous mission initialized:"), st.BrightWhite(goal))
	fmt.Fprintln(c.Out, st.Dim("Phase 1: Planning step-by-step approach & verification criteria..."))
	return nil
}
