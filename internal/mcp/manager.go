package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// Server states reported by the Manager.
const (
	StateConnected = "connected"
	StateFailed    = "failed"
)

// Scopes name where a server configuration came from.
const (
	ScopeGlobal  = "global"
	ScopeProject = "project"
)

// DefaultServerInitTimeout bounds the startup handshake (initialize plus
// tools/list) of one server. It is per server, so N slow servers do not share a
// single budget the way one context for the whole load did.
const DefaultServerInitTimeout = 10 * time.Second

// ServerReport is the runtime status of one configured server.
type ServerReport struct {
	Name      string
	Scope     string
	Command   string
	Args      []string
	State     string
	ToolNames []string
	Err       string
}

// ServerEntry is one configured server with the scope it came from.
type ServerEntry struct {
	Name   string
	Scope  string
	Config ServerConfig
}

// Manager owns the MCP servers of a session and reports what happened to each.
// Servers are independent: one that fails to start is reported as failed and the
// rest still load.
type Manager struct {
	mu          sync.Mutex
	reports     []ServerReport
	clients     []*Client
	tools       []tools.Tool
	initTimeout time.Duration
	callTimeout time.Duration
}

func NewManager() *Manager {
	return &Manager{
		initTimeout: DefaultServerInitTimeout,
		callTimeout: DefaultToolCallTimeout,
	}
}

// InitAll loads every configured server, best effort, and always returns a
// Manager. Failure is reported per server instead of aborting the whole load,
// which is what let a single broken entry take down every server behind it.
func InitAll(ctx context.Context, configDir, cwd string) *Manager {
	m := NewManager()
	// seen carries the model-facing names already taken, because two distinct
	// server or tool names can sanitize into the same function name.
	seen := map[string]bool{}
	for _, entry := range LoadServerConfigsOrdered(configDir, cwd) {
		m.load(ctx, entry, seen)
	}
	return m
}

// load starts one server and records its status. It never returns an error: a
// failure belongs to this server alone.
func (m *Manager) load(ctx context.Context, entry ServerEntry, seen map[string]bool) {
	report := ServerReport{
		Name:    entry.Name,
		Scope:   entry.Scope,
		Command: entry.Config.Command,
		Args:    append([]string(nil), entry.Config.Args...),
	}
	defer func() { m.reports = append(m.reports, report) }()

	if strings.TrimSpace(entry.Config.Command) == "" {
		report.State = StateFailed
		report.Err = "no command configured"
		return
	}

	callCtx, cancel := context.WithTimeout(ctx, m.initTimeout)
	defer cancel()

	client := New(entry.Name, entry.Config.Command, entry.Config.Args, envSlice(entry.Config.Env))
	if err := client.Start(); err != nil {
		report.State = StateFailed
		report.Err = err.Error()
		return
	}
	if err := client.Initialize(callCtx); err != nil {
		client.Stop()
		report.State = StateFailed
		report.Err = err.Error()
		return
	}
	defs, err := client.ListTools(callCtx)
	if err != nil {
		client.Stop()
		report.State = StateFailed
		report.Err = err.Error()
		return
	}

	report.State = StateConnected
	m.clients = append(m.clients, client)
	wrapped := m.wrapAll(client, entry.Name, defs, seen)
	m.tools = append(m.tools, wrapped...)
	report.ToolNames = make([]string, 0, len(wrapped))
	for _, w := range wrapped {
		report.ToolNames = append(report.ToolNames, w.Name())
	}
}

// wrapAll exposes one server's tools to the model under unique, well-formed
// names.
func (m *Manager) wrapAll(client *Client, server string, defs []ToolDef, seen map[string]bool) []tools.Tool {
	wrapped := make([]tools.Tool, 0, len(defs))
	for _, def := range defs {
		w := WrapTool(client, server, def)
		w.callTimeout = m.callTimeout
		w.wrapped = uniqueToolName(seen, w.Name())
		wrapped = append(wrapped, w)
	}
	return wrapped
}

// uniqueToolName returns base, or base with a numeric suffix when an earlier
// server already took it.
func uniqueToolName(seen map[string]bool, base string) string {
	if !seen[base] {
		seen[base] = true
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s_%d", base, i)
		if !seen[candidate] {
			seen[candidate] = true
			return candidate
		}
	}
}

// Tools returns the model-facing tools of every connected server.
func (m *Manager) Tools() []tools.Tool {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]tools.Tool, len(m.tools))
	copy(out, m.tools)
	return out
}

// Reports returns the status of every configured server, in load order
// (project scope first, then by name).
func (m *Manager) Reports() []ServerReport {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ServerReport, len(m.reports))
	copy(out, m.reports)
	return out
}

// Total reports how many servers are configured.
func (m *Manager) Total() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.reports)
}

// Connected reports how many servers completed the handshake.
func (m *Manager) Connected() int {
	return m.countState(StateConnected)
}

// Failed reports how many servers could not be started.
func (m *Manager) Failed() int {
	return m.countState(StateFailed)
}

func (m *Manager) countState(state string) int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.reports {
		if r.State == state {
			n++
		}
	}
	return n
}

// Stop shuts every running server down. It is safe to call twice.
func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	clients := m.clients
	m.clients = nil
	m.mu.Unlock()
	for _, c := range clients {
		c.Stop()
	}
}

// LoadServerConfigsOrdered returns every configured server with the scope it
// came from, ordered project-first and then by name so startup and reports are
// deterministic. A server defined in both files appears once, with the project
// definition winning.
func LoadServerConfigsOrdered(configDir, cwd string) []ServerEntry {
	merged := map[string]ServerEntry{}
	for _, src := range []struct {
		scope string
		path  string
	}{
		{ScopeGlobal, filepath.Join(configDir, "mcp.json")},
		{ScopeProject, filepath.Join(cwd, ".vibe-coder", "mcp.json")},
	} {
		// A symlinked config file is ignored: it can point outside the project
		// and silently redefine which servers a run starts.
		info, err := os.Lstat(src.path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		data, err := os.ReadFile(src.path)
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
			merged[name] = ServerEntry{Name: name, Scope: src.scope, Config: cfg}
		}
	}

	entries := make([]ServerEntry, 0, len(merged))
	for _, entry := range merged {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if scopeRank(entries[i].Scope) != scopeRank(entries[j].Scope) {
			return scopeRank(entries[i].Scope) < scopeRank(entries[j].Scope)
		}
		return entries[i].Name < entries[j].Name
	})
	return entries
}

// LoadServerConfigs returns the merged view (name to config), project
// definitions overriding global ones.
func LoadServerConfigs(configDir, cwd string) map[string]ServerConfig {
	merged := map[string]ServerConfig{}
	for _, entry := range LoadServerConfigsOrdered(configDir, cwd) {
		merged[entry.Name] = entry.Config
	}
	return merged
}

// scopeRank orders project servers before global ones.
func scopeRank(scope string) int {
	if scope == ScopeProject {
		return 0
	}
	return 1
}

// envSlice renders a server's environment as the KEY=VALUE list exec expects,
// sorted so the process arguments stay deterministic.
func envSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
