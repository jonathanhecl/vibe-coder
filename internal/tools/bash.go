package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/safety"
)

const (
	defaultBashTimeoutMS = int64(120000)
	maxBashTimeoutMS     = int64(600000)
)

// bashDefaultTimeoutMS is the fallback command timeout. It can be raised for
// slow machines/large builds with VIBE_BASH_TIMEOUT_MS (still capped at 10m).
func bashDefaultTimeoutMS() int64 {
	if raw := strings.TrimSpace(os.Getenv("VIBE_BASH_TIMEOUT_MS")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			if parsed > maxBashTimeoutMS {
				return maxBashTimeoutMS
			}
			return parsed
		}
	}
	return defaultBashTimeoutMS
}

type BashTool struct {
	// maxOut caps the combined stdout+stderr returned to the model.
	// Zero keeps the historical 30KB head/tail budget.
	maxOut int
}

func NewBashTool() *BashTool { return &BashTool{} }

// SetMaxOutputBytes sets the output cap; output longer than it is kept as
// head and tail halves with a truncation marker in between.
func (t *BashTool) SetMaxOutputBytes(n int) { t.maxOut = n }

func (t *BashTool) outputLimit() int {
	if t.maxOut > 0 {
		return t.maxOut
	}
	return defaultShellOutputBytes
}

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
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Maximum run time in milliseconds (default 120000, max 600000). Raise it for slow builds or test suites.",
					},
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

	timeoutMS := bashDefaultTimeoutMS()
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
		timeoutMS = bashDefaultTimeoutMS()
	}
	if timeoutMS > maxBashTimeoutMS {
		timeoutMS = maxBashTimeoutMS
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
	// stdout and stderr go to separate buffers: the tool needs to know whether
	// the command printed usable output to stdout or only its own failure
	// message to stderr. Two distinct writers also keep os/exec from copying
	// both file descriptors concurrently into one unsynchronized buffer.
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	output := stdoutBuf.String()
	if stderrBuf.Len() > 0 {
		if output != "" && !strings.HasSuffix(output, "\n") {
			output += "\n"
		}
		output += stderrBuf.String()
	}
	if output == "" && err == nil && stdoutBuf.Len() == 0 {
		output = "(no output)"
	}
	output = TruncateOutput(output, t.outputLimit())
	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			seconds := time.Duration(timeoutMS) * time.Millisecond
			return Result{
				Output: fmt.Sprintf("%s\n(command timed out after %s; rerun with a larger \"timeout\" in milliseconds, max %d, or use a faster command)",
					strings.TrimRight(output, "\n"), seconds, maxBashTimeoutMS),
				HintsForModel: fmt.Sprintf("The command exceeded %s and was killed. If it was a build or test that legitimately needs longer, call Bash again with {\"timeout\": 600000}. Otherwise split the work into smaller commands.", seconds),
				IsError:       true,
			}
		}
		trimmed := strings.TrimRight(output, "\n")
		code := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		if code > 0 && stdoutBuf.Len() > 0 && strings.TrimSpace(trimmed) != "" {
			// The command ran and printed to stdout: a non-zero status describes
			// the last command in the chain, not a failed tool call. The model
			// still sees the code, but the card is not an error and the output is
			// treated as evidence (a diagnostic sed/ls/cat chain often ends on a
			// missing file and that is not what the call was for). Stderr-only
			// failures keep the error card: the command itself failed.
			return Result{
				Output:        fmt.Sprintf("%s\n(exit %d; the output above is what the command printed)", trimmed, code),
				HintsForModel: "The command exited non-zero but printed output. Read the output before treating it as a failure; with ';' and '&&' chains only the last command sets the status, and a missing file at the end can mask a successful inspection. Re-run just the step you need if the status hides the real result.",
			}
		}
		return Result{
			Output:  fmt.Sprintf("%s\n(exit error: %v)", trimmed, err),
			IsError: true,
		}
	}
	return Result{Output: strings.TrimRight(output, "\n")}
}
