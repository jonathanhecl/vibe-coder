package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

type ToolWrap struct {
	client *Client
	server string
	tool   ToolDef
}

func WrapTool(client *Client, server string, tool ToolDef) *ToolWrap {
	return &ToolWrap{
		client: client,
		server: server,
		tool:   tool,
	}
}

func (t *ToolWrap) Name() string {
	return "mcp_" + t.server + "_" + t.tool.Name
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

func (t *ToolWrap) Execute(ctx context.Context, params map[string]any) tools.Result {
	out, err := t.client.CallTool(ctx, t.tool.Name, params)
	if err != nil {
		return tools.Result{Output: err.Error(), IsError: true}
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
