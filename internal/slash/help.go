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
			{"/new", "save the current session and start a brand new one"},
			{"/clear", "show clear options (session, sessions, context)"},
			{"/clear session", "discard the current session without saving and start fresh"},
			{"/clear sessions", "delete ALL saved sessions (asks Y/n)"},
			{"/clear context", "unpin all persistent context files"},
			{"/sessions", "list saved sessions (* = current project)"},
			{"/session <id>", "resume a specific session quickly"},
			{"/session last", "resume the most recently modified session"},
			{"/sessions delete <id>", "delete a specific session"},
			{"/sessions delete --all", "delete every saved session"},
			{"/resume", "resume the last session for this project path"},
			{"/compact", "force a sidecar-summarized compaction"},
			{"/tokens", "show token usage vs the context window"},
			{"/status", "model, cwd, session and sidecar status"},
			{"/context <file>", "pin a .md/.txt guide as persistent session instruction"},
			{"/context list", "show pinned context files"},
		}},
		{"Model", [][2]string{
			{"/model", "show the active model"},
			{"/model <name>", "switch the active model for this run"},
			{"/think", "show the thinking level and model capability"},
			{"/think off|low|medium|high|max|on", "set thinking effort for this session"},
			{"/sidecar on|off", "toggle the sidecar for this session"},
			{"/sidecar perm-on|perm-off", "persist sidecar state to vibe-coder.env"},
			{"/sidecar status", "show current sidecar state"},
			{"/jevstyle", "show or set JEV Style decision model"},
			{"/jevstyle <name>", "set JEV Style model for this session"},
			{"/jevstyle off", "disable JEV Style for this session"},
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
			{"/review <prompt>", "ask the model in read-only mode (no edits, no commands)"},
		}},
		{"Git", [][2]string{
			{"/commit", "stage + commit current changes (LLM-suggested message)"},
		}},
		{"Misc", [][2]string{
			{"/help, /commands", "show this help"},
			{"/exit, /quit, /q, bye", "save and exit (Ctrl+D also exits)"},
			{"ESC ESC (double-tap)", "stop the running agent and return to prompt"},
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
