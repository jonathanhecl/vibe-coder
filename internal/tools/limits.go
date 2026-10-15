package tools

import (
	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// defaultShellOutputBytes is the historical head/tail truncation budget for
// shell-family tools when no configured limit applies (30KB total).
const defaultShellOutputBytes = 30 * 1024

// outputLimiter is implemented by tools whose model-facing output can be
// capped. Registry.SetOutputLimit applies one shared cap to all of them.
type outputLimiter interface {
	SetMaxOutputBytes(int)
}

// OutputLimitBytes converts the harness tuning knob into bytes: an explicit
// Harness.ToolOutputMaxTokens wins; zero means auto, priced at 15% of the
// context window with a 2000-token floor. Four bytes approximate one token.
func OutputLimitBytes(cfg *config.Config) int {
	if cfg == nil {
		return 0
	}
	tokens := cfg.Harness.ToolOutputMaxTokens
	if tokens <= 0 {
		tokens = cfg.ContextWindow * 15 / 100
		if tokens < 2000 {
			tokens = 2000
		}
	}
	return tokens * 4
}

// TruncateOutput keeps the first and last halves of s within maxBytes so
// both the beginning (often the error headline) and the end (the actual
// failure) survive. maxBytes <= 0 disables truncation.
//
// It is exported because tools implemented outside this package reuse it: the
// MCP wrapper receives the same cap through Registry.SetOutputLimit, so one
// truncation behavior serves every tool.
func TruncateOutput(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	head := maxBytes / 2
	tail := maxBytes - head
	return s[:head] + "\n... (truncated) ...\n" + s[len(s)-tail:]
}
