package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/session"
)

// Wire-time observation masking: tool outputs older than the last K tool
// turns are replaced with a stub at send time. The stored transcript keeps
// the full output — masking only shapes what the model sees.
const (
	// maskMinOutputLen is the byte length under which a tool output is too
	// small to bother masking.
	maskMinOutputLen = 400
	// maskFirstLineRunes bounds the "first line" hint inside the stub.
	maskFirstLineRunes = 120
	// maskHysteresis slack: masking only recomputes once more than K+4 tool
	// turns are unmasked, so maskBefore does not crawl forward one turn per
	// request.
	maskHysteresis = 4
)

var toolResultEnvelopeRe = regexp.MustCompile(`(?s)^\[tool_result name=([^\]\n]+)\]\n?(.*)$`)

// wireState is the agent's transcript-keyed send window bookkeeping. gen is
// the session generation the state was computed for; maskBefore is the
// history index before which tool results are masked; start is the sticky
// window start.
type wireState struct {
	set        bool
	gen        uint64
	maskBefore int
	start      int
}

func isToolResult(m session.Message) bool {
	return m.Role == "tool" ||
		(m.Role == "user" && strings.HasPrefix(m.Content, "[tool_result"))
}

// toolTurnStarts returns the turn-start indices of every turn that contains
// at least one tool result, in order. A turn starts at each assistant message
// whose content does not begin with "[runtime]"; results appearing before the
// first assistant message belong to turn -1.
func toolTurnStarts(hist []session.Message) []int {
	var out []int
	turn := -1
	hasResult := false
	for i, m := range hist {
		if m.Role == "assistant" && !strings.HasPrefix(m.Content, "[runtime]") {
			if hasResult {
				out = append(out, turn)
			}
			turn = i
			hasResult = false
			continue
		}
		if isToolResult(m) {
			hasResult = true
		}
	}
	if hasResult {
		out = append(out, turn)
	}
	return out
}

// maskBeforeForTurns returns the history index that leaves exactly the k most
// recent tool turns unmasked. -1 (or any non-positive value) masks nothing.
func maskBeforeForTurns(starts []int, k int) int {
	if k < 1 {
		k = 1
	}
	if len(starts) <= k {
		return -1
	}
	return starts[len(starts)-k]
}

// maskToolResults returns a copy of hist with tool results at indexes below
// maskBefore replaced by stubs (outputs of maskMinOutputLen bytes or less are
// kept). The input slice's messages are never mutated.
func maskToolResults(hist []session.Message, maskBefore int) ([]session.Message, int) {
	if maskBefore <= 0 {
		return hist, 0
	}
	out := make([]session.Message, len(hist))
	copy(out, hist)
	masked := 0
	for i := 0; i < maskBefore && i < len(out); i++ {
		m := out[i]
		if !isToolResult(m) || len(m.Content) <= maskMinOutputLen {
			continue
		}
		if m.Role == "tool" {
			name := m.ToolName
			if name == "" {
				name = "unknown"
			}
			m.Content = elidedToolBody(name, m.Content)
		} else {
			name, inner := parseToolResultEnvelope(m.Content)
			if name == "" {
				name = "unknown"
			}
			body := inner
			if body == "" {
				body = m.Content
			}
			m.Content = session.ToolObservationUserContent(name, elidedToolBody(name, body))
		}
		out[i] = m
		masked++
	}
	return out, masked
}

// parseToolResultEnvelope extracts the tool name and inner output from a
// user-role "[tool_result name=X]…[/tool_result]" envelope.
func parseToolResultEnvelope(content string) (name, inner string) {
	m := toolResultEnvelopeRe.FindStringSubmatch(content)
	if m == nil {
		return "", ""
	}
	inner = m[2]
	if idx := strings.Index(inner, "[/tool_result]"); idx >= 0 {
		inner = inner[:idx]
	}
	return strings.TrimSpace(m[1]), strings.TrimSpace(inner)
}

// elidedToolBody builds the stub text that replaces a masked output.
func elidedToolBody(name, original string) string {
	first := ""
	for _, line := range strings.Split(original, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			first = t
			break
		}
	}
	if r := []rune(first); len(r) > maskFirstLineRunes {
		first = string(r[:maskFirstLineRunes])
	}
	return fmt.Sprintf("[elided] %s output (%d chars) from an earlier step. First line: %s",
		name, len(original), first)
}

// keepToolResults returns the configured number of recent tool turns to keep
// unmasked (default 6).
func (a *Agent) keepToolResults() int {
	k := 6
	if a != nil && a.cfg != nil && a.cfg.Harness.KeepToolResults > 0 {
		k = a.cfg.Harness.KeepToolResults
	}
	return k
}

// maskBeforeLocked resolves the masking frontier with hysteresis. A new
// session generation recomputes exactly K unmasked tool turns and resets the
// sticky window start; within a generation maskBefore is retained (and can
// only move forward) until more than K+4 turns sit unmasked. Caller must hold
// a.wireMu.
func (a *Agent) maskBeforeLocked(hist []session.Message, gen uint64) int {
	k := a.keepToolResults()
	st := &a.wire
	if !st.set || st.gen != gen {
		st.set = true
		st.gen = gen
		st.start = 0
		st.maskBefore = maskBeforeForTurns(toolTurnStarts(hist), k)
		return st.maskBefore
	}
	unmasked := 0
	starts := toolTurnStarts(hist)
	for _, s := range starts {
		if s >= st.maskBefore {
			unmasked++
		}
	}
	if unmasked > k+maskHysteresis {
		if nb := maskBeforeForTurns(starts, k); nb > st.maskBefore {
			st.maskBefore = nb
		}
	}
	return st.maskBefore
}

// wireMaskBefore applies hysteresis and returns the masked copy of hist.
// Intended for buildOllamaMessages; compaction uses maskBeforeForState to
// peek without taking the hysteresis lock.
func (a *Agent) maskBeforeForState(hist []session.Message, gen uint64) int {
	a.wireMu.Lock()
	defer a.wireMu.Unlock()
	if a.wire.set && a.wire.gen == gen {
		return a.wire.maskBefore
	}
	return maskBeforeForTurns(toolTurnStarts(hist), a.keepToolResults())
}
