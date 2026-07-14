package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func printHelp(c *Ctx) {
	st := tui.NewStyle(c.Out)
	fmt.Fprintln(c.Out, st.BoldCyan("vibe-coder commands"))
	groups := []struct {
		title string
		items [][2]string
	}{
		{"Session", [][2]string{
			{"/save", "persist the current session to disk"},
			{"/clear", "save and start a brand new session"},
			{"/sessions", "list saved sessions (* = current project)"},
			{"/session <id>", "resume a specific session quickly"},
			{"/sessions delete <id>", "delete a specific session"},
			{"/sessions delete --all", "delete every saved session"},
			{"/resume", "resume the last session for this project path"},
			{"/resume <id>", "resume a specific session by id (or unique prefix)"},
			{"/compact", "force a sidecar-summarized compaction"},
			{"/tokens", "show token usage vs the context window"},
			{"/status", "model, cwd, session and sidecar status"},
		}},
		{"Model", [][2]string{
			{"/model", "show the active model"},
			{"/model <name>", "switch the active model for this run"},
			{"/sidecar on|off", "toggle the sidecar for this session"},
			{"/sidecar perm-on|perm-off", "persist sidecar state to vibe-coder.env"},
			{"/sidecar status", "show current sidecar state"},
			{"/hide-think", "hide model thinking blocks in CLI output"},
			{"/show-think", "show model thinking blocks in CLI output (default)"},
		}},
		{"Mode", [][2]string{
			{"/yes", "auto-approve subsequent permission prompts"},
			{"/allow_all true|false", "toggle auto-approve (same as /yes and /no)"},
			{"/no", "require manual approval (default)"},
			{"/plan", "enter plan mode (yellow prompt, writes restricted to .vibe-coder/plans)"},
			{"/plan <goal>", "enter plan mode and immediately start planning that goal"},
			{"/code", "exit plan mode and return to coding mode"},
			{"/approve", "exit plan mode and resume act mode in the same chat"},
		}},
		{"Git", [][2]string{
			{"/commit", "stage + commit current changes (LLM-suggested message)"},
		}},
		{"Misc", [][2]string{
			{"/help, /commands", "show this help"},
			{"/exit, /quit, /q", "save and exit"},
		}},
	}
	maxCmd := 0
	for _, g := range groups {
		for _, it := range g.items {
			if l := len(it[0]); l > maxCmd {
				maxCmd = l
			}
		}
	}
	for i, g := range groups {
		if i > 0 {
			fmt.Fprintln(c.Out)
		}
		fmt.Fprintln(c.Out, st.BoldYellow(g.title))
		for _, it := range g.items {
			pad := strings.Repeat(" ", maxCmd-len(it[0]))
			fmt.Fprintf(c.Out, "  %s%s  %s\n",
				st.Green(it[0]),
				pad,
				st.Dim(it[1]),
			)
		}
	}
}
