package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

func LoadServerConfigs(configDir, cwd string) map[string]ServerConfig {
	merged := map[string]ServerConfig{}
	for _, path := range []string{
		filepath.Join(configDir, "mcp.json"),
		filepath.Join(cwd, ".vibe-coder", "mcp.json"),
	} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw struct {
			MCPServers map[string]ServerConfig `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}
		for name, cfg := range raw.MCPServers {
			merged[name] = cfg
		}
	}
	return merged
}

func InitAndWrapAll(ctx context.Context, configDir, cwd string) ([]*Client, []tools.Tool, error) {
	cfgs := LoadServerConfigs(configDir, cwd)
	clients := make([]*Client, 0, len(cfgs))
	wrapped := make([]tools.Tool, 0, 8)
	for name, cfg := range cfgs {
		if strings.TrimSpace(cfg.Command) == "" {
			continue
		}
		envv := make([]string, 0, len(cfg.Env))
		for k, v := range cfg.Env {
			envv = append(envv, k+"="+v)
		}
		client := New(name, cfg.Command, cfg.Args, envv)
		if err := client.Start(); err != nil {
			return clients, wrapped, err
		}
		if err := client.Initialize(ctx); err != nil {
			client.Stop()
			return clients, wrapped, err
		}
		toolsList, err := client.ListTools(ctx)
		if err != nil {
			client.Stop()
			return clients, wrapped, err
		}
		clients = append(clients, client)
		for _, toolDef := range toolsList {
			wrapped = append(wrapped, WrapTool(client, name, toolDef))
		}
	}
	return clients, wrapped, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func DefaultInitContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}
