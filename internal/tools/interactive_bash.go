package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/safety"
	"github.com/jonathanhecl/vibe-coder/internal/terminal"
)

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inEscape {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				inEscape = false
			}
			continue
		}
		if c == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			inEscape = true
			i++
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// InteractiveBashTool runs a shell command in an interactive session.
// If the command stays alive waiting for input, it returns partial output
// together with a session_id so follow-up SendInput calls can respond.
type InteractiveBashTool struct {
	mgr *terminal.Manager
}

func NewInteractiveBashTool() *InteractiveBashTool {
	return &InteractiveBashTool{mgr: terminal.DefaultManager()}
}

func (t *InteractiveBashTool) Name() string { return "InteractiveBash" }
func (t *InteractiveBashTool) Description() string {
	return "Run a shell command interactively. Use this for commands that prompt for input (Y/n, choices, passwords). Returns partial output and a session_id if the command is still waiting for input. Follow up with SendInput to provide responses."
}

func (t *InteractiveBashTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "The shell command to run interactively"},
					"timeout": map[string]any{"type": "integer", "description": "Max time in milliseconds to wait for the command to finish (default 120000)"},
				},
				"required": []string{"command"},
			},
		},
	}
}

func (t *InteractiveBashTool) Execute(ctx context.Context, params map[string]any) Result {
	command, ok := params["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return errResult("command is required")
	}
	if safety.IsBackgroundCommand(command) {
		return errResult("background commands are blocked")
	}
	if blocked, reason := safety.IsDangerousCommand(command); blocked {
		return errResult("dangerous command blocked: " + reason)
	}
	if safety.IsProtectedWrite(command) {
		return errResult("protected config writes are blocked")
	}

	timeoutMS := int64(120000)
	switch v := params["timeout"].(type) {
	case float64:
		timeoutMS = int64(v)
	case int:
		timeoutMS = int64(v)
	case string:
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			timeoutMS = parsed
		}
	}
	if timeoutMS <= 0 {
		timeoutMS = 120000
	}
	if timeoutMS > 600000 {
		timeoutMS = 600000
	}

	_ = time.Duration(timeoutMS) * time.Millisecond // reserved for future per-session timeout

	sess, err := t.mgr.Start(command)
	if err != nil {
		return errResult(fmt.Sprintf("failed to start session: %v", err))
	}

	output, running := sess.ReadOutput(2 * time.Second)
	output = stripANSI(output)

	if !running {
		final := output + stripANSI(sess.FinalOutput())
		if final == "" {
			final = "(no output)"
		}
		_ = t.mgr.Terminate(sess.ID)
		if len(final) > 30*1024 {
			head := final[:15*1024]
			tail := final[len(final)-15*1024:]
			final = head + "\n... (truncated) ...\n" + tail
		}
		return Result{Output: strings.TrimRight(final, "\n")}
	}

	if output == "" {
		output = "(waiting for input)"
	}
	if len(output) > 30*1024 {
		head := output[:15*1024]
		tail := output[len(output)-15*1024:]
		output = head + "\n... (truncated) ...\n" + tail
	}

	return Result{
		Output: fmt.Sprintf("%s\n\n[session_id: %s]\n[status: running -- use SendInput to respond]", strings.TrimRight(output, "\n"), sess.ID),
	}
}

// SendInputTool sends a line of input to a running interactive session.
type SendInputTool struct {
	mgr *terminal.Manager
}

func NewSendInputTool() *SendInputTool {
	return &SendInputTool{mgr: terminal.DefaultManager()}
}

func (t *SendInputTool) Name() string { return "SendInput" }
func (t *SendInputTool) Description() string {
	return "Send input to a running interactive terminal session. Use the session_id returned by InteractiveBash."
}

func (t *SendInputTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string"},
					"input":      map[string]any{"type": "string", "description": "The text to send to the session's stdin"},
					"timeout":    map[string]any{"type": "integer", "description": "Time in ms to wait for output after sending input (default 2000)"},
				},
				"required": []string{"session_id", "input"},
			},
		},
	}
}

func (t *SendInputTool) Execute(ctx context.Context, params map[string]any) Result {
	sessionID, ok := params["session_id"].(string)
	if !ok || strings.TrimSpace(sessionID) == "" {
		return errResult("session_id is required")
	}

	input, ok := params["input"].(string)
	if !ok {
		return errResult("input is required")
	}

	sess := t.mgr.Get(sessionID)
	if sess == nil {
		return errResult("session not found: " + sessionID)
	}

	if !sess.IsRunning() {
		return errResult("session has already exited: " + sessionID)
	}

	if err := sess.SendInput(input); err != nil {
		return errResult(fmt.Sprintf("failed to send input: %v", err))
	}

	timeoutMS := int64(2000)
	switch v := params["timeout"].(type) {
	case float64:
		timeoutMS = int64(v)
	case int:
		timeoutMS = int64(v)
	case string:
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			timeoutMS = parsed
		}
	}
	if timeoutMS <= 0 {
		timeoutMS = 2000
	}
	if timeoutMS > 60000 {
		timeoutMS = 60000
	}

	output, running := sess.ReadOutput(time.Duration(timeoutMS) * time.Millisecond)
	output = stripANSI(output)

	if !running {
		if output == "" {
			output = "(session exited, no additional output)"
		}
		_ = t.mgr.Terminate(sessionID)
		if len(output) > 30*1024 {
			head := output[:15*1024]
			tail := output[len(output)-15*1024:]
			output = head + "\n... (truncated) ...\n" + tail
		}
		return Result{Output: strings.TrimRight(output, "\n")}
	}

	if output == "" {
		output = "(waiting for more input)"
	}
	if len(output) > 30*1024 {
		head := output[:15*1024]
		tail := output[len(output)-15*1024:]
		output = head + "\n... (truncated) ...\n" + tail
	}

	return Result{
		Output: fmt.Sprintf("%s\n\n[session_id: %s]\n[status: running -- use SendInput to continue or TerminateSession to kill]", strings.TrimRight(output, "\n"), sessionID),
	}
}

// TerminateSessionTool kills a running interactive session and returns any
// remaining output.
type TerminateSessionTool struct {
	mgr *terminal.Manager
}

func NewTerminateSessionTool() *TerminateSessionTool {
	return &TerminateSessionTool{mgr: terminal.DefaultManager()}
}

func (t *TerminateSessionTool) Name() string { return "TerminateSession" }
func (t *TerminateSessionTool) Description() string {
	return "Kill a running interactive terminal session and return any remaining output."
}

func (t *TerminateSessionTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string"},
				},
				"required": []string{"session_id"},
			},
		},
	}
}

func (t *TerminateSessionTool) Execute(ctx context.Context, params map[string]any) Result {
	sessionID, ok := params["session_id"].(string)
	if !ok || strings.TrimSpace(sessionID) == "" {
		return errResult("session_id is required")
	}

	sess := t.mgr.Get(sessionID)
	if sess == nil {
		return errResult("session not found: " + sessionID)
	}

	remaining, _ := sess.ReadOutput(2 * time.Second)
	remaining = stripANSI(remaining)

	if err := t.mgr.Terminate(sessionID); err != nil {
		return errResult(fmt.Sprintf("failed to terminate session: %v", err))
	}

	if remaining == "" {
		remaining = "(session terminated)"
	}
	return Result{Output: strings.TrimRight(remaining, "\n")}
}
