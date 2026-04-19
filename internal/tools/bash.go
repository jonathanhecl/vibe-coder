package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/safety"
)

type BashTool struct{}

func NewBashTool() *BashTool { return &BashTool{} }

func (t *BashTool) Name() string { return "Bash" }
func (t *BashTool) Description() string {
	return "Run a shell command."
}
func (t *BashTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string"},
					"timeout": map[string]any{"type": "integer"},
				},
				"required": []string{"command"},
			},
		},
	}
}

func (t *BashTool) Execute(ctx context.Context, params map[string]any) Result {
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
	cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cmdCtx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(cmdCtx, "sh", "-c", command)
	}
	cmd.Env = safety.CleanEnv()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	output := out.String()
	if output == "" && err == nil {
		output = "(no output)"
	}
	if len(output) > 30*1024 {
		head := output[:15*1024]
		tail := output[len(output)-15*1024:]
		output = head + "\n... (truncated) ...\n" + tail
	}
	if err != nil {
		return Result{
			Output:  fmt.Sprintf("%s\n(exit error: %v)", strings.TrimRight(output, "\n"), err),
			IsError: true,
		}
	}
	return Result{Output: strings.TrimRight(output, "\n")}
}
