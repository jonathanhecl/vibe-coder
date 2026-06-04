package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
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
		if td.Name == "" {
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
