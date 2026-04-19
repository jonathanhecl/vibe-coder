package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

type Client struct {
	name    string
	command string
	args    []string
	envv    []string

	cmd    *exec.Cmd
	stdin  *bufio.Writer
	stdout *bufio.Reader

	mu     sync.Mutex
	nextID int64
}

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type ServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func New(name, command string, args, envv []string) *Client {
	return &Client{
		name:    name,
		command: command,
		args:    append([]string(nil), args...),
		envv:    append([]string(nil), envv...),
		nextID:  1,
	}
}

func (c *Client) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil {
		return nil
	}

	cmd := exec.Command(c.command, c.args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), c.envv...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start mcp server %s: %w", c.name, err)
	}

	c.cmd = cmd
	c.stdin = bufio.NewWriter(stdin)
	c.stdout = bufio.NewReader(stdout)
	return nil
}

func (c *Client) Initialize(ctx context.Context) error {
	initParams := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "vibe-coder",
			"version": "dev",
		},
	}
	if _, err := c.call(ctx, "initialize", initParams); err != nil {
		return err
	}
	_ = c.notify("notifications/initialized", map[string]any{})
	return nil
}

func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error) {
	resAny, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	resultMap, ok := resAny.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid tools/list result shape")
	}
	rawTools, _ := resultMap["tools"].([]any)
	out := make([]ToolDef, 0, len(rawTools))
	for _, item := range rawTools {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		td := ToolDef{
			Name:        asString(m["name"]),
			Description: asString(m["description"]),
			InputSchema: asMap(m["inputSchema"]),
		}
		if strings.TrimSpace(td.Name) == "" {
			continue
		}
		if td.InputSchema == nil {
			td.InputSchema = map[string]any{"type": "object"}
		}
		out = append(out, td)
	}
	return out, nil
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	resAny, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return "", err
	}
	resultMap, ok := resAny.(map[string]any)
	if !ok {
		return "", fmt.Errorf("invalid tools/call result")
	}
	content, _ := resultMap["content"].([]any)
	if len(content) == 0 {
		raw, _ := json.Marshal(resultMap)
		return string(raw), nil
	}
	parts := make([]string, 0, len(content))
	for _, item := range content {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if asString(m["type"]) == "text" {
			parts = append(parts, asString(m["text"]))
		}
	}
	if len(parts) == 0 {
		raw, _ := json.Marshal(resultMap)
		return string(raw), nil
	}
	return strings.Join(parts, "\n"), nil
}

func (c *Client) Stop() {
	c.mu.Lock()
	cmd := c.cmd
	c.cmd = nil
	c.stdin = nil
	c.stdout = nil
	c.mu.Unlock()
	if cmd == nil {
		return
	}
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_, _ = cmd.Process.Wait()
}

func (c *Client) call(ctx context.Context, method string, params any) (any, error) {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.mu.Unlock()

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := c.write(req); err != nil {
		return nil, err
	}
	for {
		resp, err := c.read(ctx)
		if err != nil {
			return nil, err
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp %s error: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *Client) notify(method string, params any) error {
	req := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	return c.write(req)
}

func (c *Client) write(req rpcRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return fmt.Errorf("mcp client not started")
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := c.stdin.Write(append(raw, '\n')); err != nil {
		return err
	}
	return c.stdin.Flush()
}

func (c *Client) read(ctx context.Context) (rpcResponse, error) {
	type result struct {
		resp rpcResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		c.mu.Lock()
		reader := c.stdout
		c.mu.Unlock()
		if reader == nil {
			ch <- result{err: fmt.Errorf("mcp stdout unavailable")}
			return
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			ch <- result{err: err}
			return
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			ch <- result{err: fmt.Errorf("decode mcp response: %w", err)}
			return
		}
		ch <- result{resp: resp}
	}()

	select {
	case <-ctx.Done():
		return rpcResponse{}, ctx.Err()
	case res := <-ch:
		return res.resp, res.err
	}
}

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
