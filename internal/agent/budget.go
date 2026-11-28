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
	if a.cfg != nil {
		if a.cfg.MaxTokens > 0 {
			b.OutputReserve = a.cfg.MaxTokens
		} else if a.cfg.ContextWindow > 0 {
			// No explicit cap: a single reply may carry a whole large file, so
			// the reserve follows the window instead of a fixed 4096.
			b.OutputReserve = a.cfg.ContextWindow / 4
		}
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

// defaultCompactAtPercent mirrors config's default: compact once the masked
// transcript fills 85% of its budget.
const defaultCompactAtPercent = 85

// transcriptFullAt is the token count where the transcript counts as full: the
// configured percentage of the transcript budget. Compaction triggers there
// and the wire window is cut there, so the model never loses a message the
// compactor would still have kept. Two different numbers let the cheaper one
// win, and the cheaper one is always the lossy one: the window drops history
// without summarizing, while compaction at least leaves a summary behind.
func (a *Agent) transcriptFullAt(budget int) int {
	if budget <= 0 {
		return 0
	}
	pct := defaultCompactAtPercent
	if a != nil && a.cfg != nil {
		if a.cfg.Harness.CompactAtPercent > 0 {
			pct = a.cfg.Harness.CompactAtPercent
		}
		// Experimental RAG-for-session-history mode compacts sooner: the raw
		// older turns are demoted into the in-memory store and recalled on
		// demand, while only a recent verbatim tail plus a summary stay on the
		// wire.
		if a.cfg.SessionHistoryRAGEnabled() && pct > 50 {
			pct = 50
		}
	}
	return budget * pct / 100
}

// ContextPressure reports the calibrated tokens the masked transcript occupies
// right now and the point where compaction fires, so /stats can show how full
// the context is while it happens instead of only in the JSONL afterwards. A
// zero limit means the budget is unknown (no context window configured).
func (a *Agent) ContextPressure() (used, limit int) {
	if a == nil || a.sess == nil || a.cfg == nil {
		return 0, 0
	}
	hist := a.sess.MessagesReadOnly()
	if len(hist) == 0 {
		return 0, 0
	}
	budget := a.computeBudget(a.buildSystemPrompt())
	masked, _ := maskToolResults(hist, a.maskBeforeForState(hist, a.sess.Generation(), budget.Transcript))
	return a.calibratedTranscriptCost(masked), a.transcriptFullAt(budget.Transcript)
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
