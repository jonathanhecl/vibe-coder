package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// missionController is the optional slice of the agent runtime that lets the
// user end an agent-managed mission from outside the model. Kept local so
// fakes without mission support keep compiling.
type missionController interface {
	missionReporter
	CompleteMission(summary string)
	BlockActiveMission(reason string)
}

// runMissionCommand implements /mission: inspect and end the agent-managed
// mission from the user's side. While a mission is active the runtime keeps
// starting turns for the agent, so this is the user's way to say "I did it
// myself" (done) or "stop working on this" (cancel) without waiting for the
// model to end the mission on its own.
func runMissionCommand(c *Ctx, args []string) error {
	mc, ok := c.Agent.(missionController)
	if !ok {
		fmt.Fprintln(c.Out, "Mission control is unavailable in this session.")
		return nil
	}
	sub := "status"
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch sub {
	case "", "status":
		printMissionStatus(c, mc.MissionSnapshot())
	case "done", "complete", "finish":
		m := mc.MissionSnapshot()
		if m.Status != tools.MissionStatusActive {
			fmt.Fprintln(c.Out, "No active mission to complete. The agent declares one with MissionStart.")
			return nil
		}
		summary := strings.TrimSpace(strings.Join(args[1:], " "))
		if summary == "" {
			summary = "Completed by the user."
		}
		mc.CompleteMission(summary)
		if c.Session != nil {
			c.Session.AddUser("[System Note] Mission completed by the user: " + summary)
		}
		fmt.Fprintf(c.Out, "Mission completed: %s\n", summary)
	case "cancel", "abort", "stop":
		m := mc.MissionSnapshot()
		if m.Status != tools.MissionStatusActive {
			fmt.Fprintln(c.Out, "No active mission to cancel.")
			return nil
		}
		reason := strings.TrimSpace(strings.Join(args[1:], " "))
		if reason == "" {
			reason = "Cancelled by the user."
		}
		mc.BlockActiveMission(reason)
		if c.Session != nil {
			c.Session.AddUser("[System Note] Mission cancelled by the user: " + reason)
		}
		fmt.Fprintf(c.Out, "Mission cancelled: %s\n", reason)
	default:
		fmt.Fprintln(c.Out, "Usage: /mission [status | done [summary] | cancel [reason]]")
	}
	return nil
}

// printMissionStatus renders the current mission state, matching the wording
// used by /status. It explains how to end an active mission so the user does
// not have to wait for the model to call MissionComplete.
func printMissionStatus(c *Ctx, m tools.Mission) {
	if m.Status == "" {
		fmt.Fprintln(c.Out, "No mission. The agent declares one with MissionStart when a task spans many turns.")
		return
	}
	fmt.Fprintf(c.Out, "Mission: %s\n", m.Status)
	if g := strings.TrimSpace(m.Goal); g != "" {
		fmt.Fprintf(c.Out, "Goal: %s\n", g)
	}
	if s := strings.TrimSpace(m.Summary); s != "" {
		fmt.Fprintf(c.Out, "Summary: %s\n", s)
	}
	if m.Turns > 0 {
		fmt.Fprintf(c.Out, "Turns: %d\n", m.Turns)
	}
	if m.Status == tools.MissionStatusActive {
		fmt.Fprintln(c.Out, "End it with: /mission done [summary]  ·  /mission cancel [reason]")
	}
}
