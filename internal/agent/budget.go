package agent

import (
	"encoding/json"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/session"
)

// contextBudget splits the configured context window into the sections a
// request actually pays for: the completion reserve, the system prompt, the
// native tool schema, a safety margin, and what is left for the transcript.
type contextBudget struct {
	Window        int
	OutputReserve int
	SystemTokens  int
	SchemaTokens  int
	Transcript    int
}

// computeBudget prices one request against cfg.ContextWindow. The transcript
// share is what remains after output reserve, system prompt, schema, and a
// 5% margin, floored so small windows still get a usable history.
func (a *Agent) computeBudget(systemPrompt string) contextBudget {
	var b contextBudget
	if a != nil && a.cfg != nil {
		b.Window = a.cfg.ContextWindow
	}
	b.OutputReserve = 4096
	if a.cfg != nil && a.cfg.MaxTokens > 0 {
		b.OutputReserve = a.cfg.MaxTokens
	}
	if b.Window > 0 && b.OutputReserve > b.Window/4 {
		b.OutputReserve = b.Window / 4
	}
	b.SystemTokens = session.EstimateTokens(systemPrompt)
	b.SchemaTokens = a.toolSchemaTokens()
	margin := b.Window * 5 / 100
	b.Transcript = b.Window - b.OutputReserve - b.SystemTokens - b.SchemaTokens - margin
	floor := 2048
	if b.Window/8 > floor {
		floor = b.Window / 8
	}
	if b.Transcript < floor {
		b.Transcript = floor
	}
	return b
}

// toolSchemaTokens estimates the native tool schema's token cost. The schema
// changes only when the registered tool set does, so the marshal + estimate
// is memoized per tool-name list.
func (a *Agent) toolSchemaTokens() int {
	if a == nil || a.reg == nil {
		return 0
	}
	key := strings.Join(a.reg.Names(), "\x1f")
	a.sysPrompt.mu.Lock()
	defer a.sysPrompt.mu.Unlock()
	if a.sysPrompt.schemaKey == key {
		return a.sysPrompt.schemaTokens
	}
	n := 0
	if raw, err := json.Marshal(a.nativeChatTools()); err == nil {
		n = session.EstimateTokens(string(raw))
	}
	a.sysPrompt.schemaKey = key
	a.sysPrompt.schemaTokens = n
	return n
}
