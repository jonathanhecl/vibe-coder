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
			{"/save", "persist model/thinking settings (and the session when permanent)"},
			{"/promote", "keep a temporal session (promote to permanent)"},
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
			{"/redo", "re-print the last assistant response (handy to recover context)"},
			{"/compact", "force a sidecar-summarized compaction"},
			{"/tokens", "show token usage vs the context window"},
			{"/status, /info", "show the current configuration (banner-style)"},
			{"/stats", "show session metrics (requests, tokens, timings)"},
			{"/context <file>", "pin a .md/.txt guide as persistent session instruction"},
			{"/context list", "show pinned context files"},
			{"/skills", "list discovered skills (name, source, description)"},
			{"/skills set", "choose extra skill folders (SKILLS_DIRS) interactively"},
			{"/tasks, /bg", "list background tasks (/tasks kill <id> to stop)"},
		}},
		{"Model", [][2]string{
			{"/models [PROVIDER]", "list a provider's models, numbered and tagged"},
			{"/think", "show the thinking level and model capability"},
			{"/think off|low|medium|high|max|on", "set thinking effort for this session"},
			{"/sidecar on|off", "toggle the sidecar for this session"},
			{"/sidecar status", "show current sidecar state"},
			{"/sidecar perm-on|perm-off", "persist sidecar state to vibe-coder.env"},
			{"/decision status", "show the decision backend and assisted mode"},
			{"/decision test", "test the decision model on a sample decision"},
			{"/decision assisted on|off", "toggle assisted execution mode"},
			{"/hide-think", "hide model thinking blocks in CLI output"},
			{"/show-think", "show model thinking blocks in CLI output (default)"},
		}},
		{"Providers & roles", [][2]string{
			{"/providers", "list providers, the model bound to each role, and unassigned fallbacks"},
			{"/provider add <NAME> [endpoint]", "register a new provider"},
			{"/provider <NAME> endpoint <url>", "set a provider endpoint (creates it when new)"},
			{"/provider <NAME> key <api-key>", "store a provider API key"},
			{"/provider <NAME> kind <ollama|openai>", "override the protocol detected from the endpoint"},
			{"/provider <NAME> test", "check a provider endpoint and count its models"},
			{"/provider <NAME> remove", "delete a provider no role is using"},
			{"/setmodel [PROVIDER] <role> <number|name>", "assign a model to main|sidecar|decision|embedding|vision"},
			{"/setmodel [PROVIDER] <role> off", "unbind a role"},
		}},
		{"Mode", [][2]string{
			{"/yes", "auto-approve subsequent permission prompts"},
			{"/allow_all true|false", "toggle auto-approve (same as /yes and /no)"},
			{"/no", "require manual approval (default)"},
			{"/offline [on|off]", "toggle offline mode (blocks network tools & external commands)"},
			{"/plan", "enter plan mode (yellow prompt, writes restricted to .vibe-coder/plans)"},
			{"/plan <goal>", "enter plan mode and immediately start planning that goal"},
			{"/code", "exit plan mode and return to coding mode"},
			{"/approve", "exit plan mode and resume act mode in the same chat"},
			{"/goal <objective>", "start an autonomous mission (plans, verifies, and executes)"},
			{"/mission", "show the agent's active mission (goal, status, turns)"},
			{"/mission start <goal>", "start an autonomous mission (alias for /goal)"},
			{"/mission done [summary]", "mark the mission complete (e.g. you did the work yourself)"},
			{"/mission cancel [reason]", "stop the mission without completing it"},
			{"/review <prompt>", "ask the model in read-only mode (no edits, no commands)"},
		}},
		{"Git", [][2]string{
			{"/commit", "stage + commit current changes (LLM-suggested message)"},
		}},
		{"Clipboard", [][2]string{
			{"/paste <prompt>", "send clipboard content (text, image, or file) to the agent"},
			{"/image <prompt>", "alias for /paste (same behavior)"},
		}},
		{"Misc", [][2]string{
			{"/mcp [tools]", "show MCP server status (add 'tools' for the wrapped tool names)"},
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
