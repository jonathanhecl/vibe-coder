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
	b.SystemTokens = a.calibrateTokens(session.EstimateTokens(systemPrompt))
	b.SchemaTokens = a.calibrateTokens(a.toolSchemaTokens())
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

func (a *Agent) calibrateTokens(raw int) int {
	if a == nil || a.sess == nil {
		return raw
	}
	return a.sess.CalibrateTokens(raw)
}

// toolSchemaTokens estimates the native tool schema's token cost. The schema
// changes only when the registered tool set or native-tool eligibility does,
// so the marshal + estimate is memoized per tool-name list.
func (a *Agent) toolSchemaTokens() int {
	if a == nil || a.reg == nil {
		return 0
	}
	tools := a.nativeChatTools()
	if len(tools) == 0 {
		return 0
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Function.Name)
	}
	key := strings.Join(names, "\x1f")
	a.sysPrompt.mu.Lock()
	defer a.sysPrompt.mu.Unlock()
	if a.sysPrompt.schemaKey == key {
		return a.sysPrompt.schemaTokens
	}
	n := 0
	if raw, err := json.Marshal(tools); err == nil {
		n = session.EstimateTokens(string(raw))
	}
	a.sysPrompt.schemaKey = key
	a.sysPrompt.schemaTokens = n
	return n
}
