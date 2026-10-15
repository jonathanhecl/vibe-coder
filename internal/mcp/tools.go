package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// DefaultToolCallTimeout bounds one model-facing call into an MCP server, so a
// server that stops answering fails the call instead of hanging the turn.
const DefaultToolCallTimeout = 120 * time.Second

type ToolWrap struct {
	client *Client
	server string
	tool   ToolDef

	// wrapped is the effective model-facing name, sanitized and deduplicated
	// by the Manager. Empty means "derive it from the server and tool names".
	wrapped string
	// callTimeout bounds a single call; zero disables the bound.
	callTimeout time.Duration
	// maxOutput caps the model-facing output; set via Registry.SetOutputLimit.
	maxOutput int
}

func WrapTool(client *Client, server string, tool ToolDef) *ToolWrap {
	return &ToolWrap{
		client:      client,
		server:      server,
		tool:        tool,
		callTimeout: DefaultToolCallTimeout,
	}
}

func (t *ToolWrap) Name() string {
	if t.wrapped != "" {
		return t.wrapped
	}
	return sanitizeToolName("mcp_" + t.server + "_" + t.tool.Name)
}

// sanitizeToolName rewrites name into the character set a function name allows.
// A server is free to advertise any tool name; the model-facing schema is not,
// so unusual characters collapse into underscores instead of producing a tool
// the model cannot call.
func sanitizeToolName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "mcp_tool"
	}
	return b.String()
}

func (t *ToolWrap) Description() string {
	return t.tool.Description
}

func (t *ToolWrap) Schema() tools.Schema {
	return tools.Schema{
		Type: "function",
		Function: tools.FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.tool.InputSchema,
		},
	}
}

// SetMaxOutputBytes implements the tools package outputLimiter contract, so
// Registry.SetOutputLimit bounds MCP output like every built-in tool.
func (t *ToolWrap) SetMaxOutputBytes(n int) { t.maxOutput = n }

func (t *ToolWrap) Execute(ctx context.Context, params map[string]any) tools.Result {
	if t.callTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.callTimeout)
		defer cancel()
	}
	out, err := t.client.CallTool(ctx, t.tool.Name, params)
	if err != nil {
		return tools.Result{
			Output:  fmt.Sprintf("mcp %s tool %q failed: %v", t.server, t.tool.Name, err),
			IsError: true,
		}
	}
	if t.maxOutput > 0 && len(out) > t.maxOutput {
		out = tools.TruncateOutput(out, t.maxOutput) + "\n[mcp output truncated to the tool output cap]"
	}
	return tools.Result{Output: out}
}
