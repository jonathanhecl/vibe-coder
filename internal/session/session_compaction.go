package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/prompt"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

const (
	compactionTimeout  = 90 * time.Second
	compactionKeepTail = 30
)

func (s *Session) Compact(ctx context.Context, force bool) error {
	if s == nil {
		return nil
	}

	s.mu.RLock()
	cfg := s.cfg
	if cfg == nil || len(s.messages) <= compactionKeepTail {
		s.mu.RUnlock()
		return nil
	}
	if !force && len(s.messages) <= 300 && s.calibrator.Adjust(s.tokenEstimate) <= int(0.7*float64(cfg.ContextWindow)) {
		s.mu.RUnlock()
		return nil
	}
	keepTail := cfg.ContextWindow / 4
	s.mu.RUnlock()
	return s.CompactTo(ctx, keepTail)
}

// CompactTo summarizes all but the newest messages whose cumulative token
// cost fits inside keepTailTokens (at least four messages are always kept).
// When the cut would start on an orphaned tool result it walks backward (up
// to ten steps) to include the assistant tool_calls that produced it. If the
// tail covers the whole transcript, nothing is older than the cut and the
// call is a no-op.
func (s *Session) CompactTo(ctx context.Context, keepTailTokens int) error {
	if s == nil {
		return nil
	}

	s.mu.RLock()
	cfg := s.cfg
	client := s.client
	if cfg == nil || len(s.messages) == 0 {
		s.mu.RUnlock()
		return nil
	}
	if keepTailTokens <= 0 {
		keepTailTokens = cfg.ContextWindow / 4
	}
	cut := len(s.messages)
	sum := 0
	for i := len(s.messages) - 1; i >= 0; i-- {
		cost := estimateMessageTokens(s.messages[i].Content)
		if sum+cost > keepTailTokens && len(s.messages)-cut >= 4 {
			break
		}
		sum += cost
		cut = i
	}
	// A kept tail that begins on a role:"tool" result lost its assistant
	// tool_calls on the wire; slide the cut back to include them when they
	// are close enough to reach.
	for steps := 0; steps < 10 && cut > 0 && s.messages[cut].Role == "tool"; steps++ {
		cut--
	}
	if cut <= 0 {
		s.mu.RUnlock()
		return nil
	}
	old := append([]Message(nil), s.messages[:cut]...)
	recent := append([]Message(nil), s.messages[cut:]...)
	firstUserIdx := s.firstUserMessageIndexLocked()
	revision := s.revision
	s.mu.RUnlock()

	// Demote the discarded history into the session memory so it can be
	// recalled by goal later instead of being lost. This runs outside the lock
	// (it embeds over the network) and never fails compaction: an unavailable
	// or failing embedder simply keeps nothing.
	s.ensureMemory().demote(ctx, demoteTexts(old))

	var summary string
	if client != nil {
		model := ""
		if cfg.SidecarInUse() {
			model = cfg.SidecarModel
		} else if strings.TrimSpace(cfg.Model) != "" {
			model = cfg.Model
		}
		if model != "" {
			compactCtx, cancel := context.WithTimeout(ctx, compactionTimeout)
			// Compaction runs on the sidecar when it is in use, otherwise on the
			// main model; keep_alive follows whichever model actually runs it.
			keepAlive := cfg.KeepAliveFor(config.RoleMain)
			if cfg.SidecarInUse() {
				keepAlive = cfg.KeepAliveFor(config.RoleSidecar)
			}
			resp, err := client.ChatSync(compactCtx, ollama.ChatRequest{
				Model: model,
				Messages: []ollama.Message{
					// The compaction prompt is a compile-time template: it tells the
					// sidecar to preserve the task, decisions, file paths, TODO state,
					// and what remains to do.
					{Role: "system", Content: prompt.Render("compaction_summary", nil)},
					{Role: "user", Content: renderMessagesForSummary(old)},
				},
				Stream:    false,
				KeepAlive: keepAlive,
			})
			cancel()
			if err == nil && strings.TrimSpace(resp.Content) != "" {
				summary = resp.Content
			}
		}
	}
	if summary == "" {
		summary = "Earlier conversation truncated to stay within context limits."
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != revision {
		return fmt.Errorf("session changed during compaction")
	}
	// Build the compacted transcript: summary, then the original first user
	// message preserved verbatim (so the agent never loses the task), then the
	// kept tail with orphaned native tool results converted to user-role
	// envelopes so the wire history stays valid (a role:"tool" message without
	// a preceding assistant tool_calls is rejected by Ollama and confuses the
	// model). The first user message is only prepended when it actually fell
	// inside the compacted region — if it already sits inside the kept tail,
	// adding it again would duplicate the request.
	newMsgs := make([]Message, 0, len(recent)+3)
	newMsgs = append(newMsgs, Message{
		Role:      "user",
		Content:   "[Earlier conversation summary]\n" + summary,
		Timestamp: time.Now().UTC(),
	})
	if firstUserIdx >= 0 && firstUserIdx < cut {
		newMsgs = append(newMsgs, s.messages[firstUserIdx])
	}
	newMsgs = append(newMsgs, normalizeKeptMessages(recent)...)
	s.messages = newMsgs
	s.recomputeTokenEstimate()
	s.revision++
	s.generation++
	return nil
}

// normalizeKeptMessages converts any role:"tool" message that lost its
// preceding assistant tool_calls (because that assistant turn was compacted)
// into a user-role observation envelope. This keeps the wire history valid
// for Ollama /api/chat and preserves the tool output for the model.
func normalizeKeptMessages(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for i, m := range msgs {
		if m.Role == "tool" {
			if i == 0 || !hasToolCalls(msgs[i-1]) {
				// Orphaned tool result: convert to user observation envelope.
				out = append(out, Message{
					Role:      "user",
					Content:   ToolObservationUserContent(m.ToolName, m.Content),
					Timestamp: m.Timestamp,
				})
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

func hasToolCalls(m Message) bool {
	return m.Role == "assistant" && len(m.ToolCalls) > 0
}

func (s *Session) recomputeTokenEstimate() {
	total := 0
	for _, msg := range s.messages {
		total += estimateMessageTokens(msg.Content)
	}
	s.tokenEstimate = total
}

// clipRunes truncates s to at most n runes, marking the cut with a clipped
// suffix so the reader knows content was removed. Rune-safe for CJK text.
func clipRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…[clipped]"
}

func renderMessagesForSummary(messages []Message) string {
	// Bound the sidecar input: at the compaction threshold the old
	// transcript can hold megabytes (verbatim tool outputs), which would
	// stall the small sidecar model past its timeout and degrade to the
	// static truncation note. Keep the most recent content so the summary
	// stays continuous with the messages kept verbatim. Each message is
	// clipped to a fixed rune budget so one giant tool output cannot crowd
	// out everything else.
	const (
		maxSummaryChars      = 48 * 1024
		maxPerMessageRunes   = 2000
		maxPriorSummaryRunes = 8000
		priorSummaryPrefix   = "[Earlier conversation summary]"
	)
	prefix := ""
	start := 0
	// A prior compaction summary is the only memory of everything already
	// summarized; always carry it into the next summary verbatim (clipped),
	// outside the newest-first budget.
	if len(messages) > 0 && strings.HasPrefix(messages[0].Content, priorSummaryPrefix) {
		prefix = "user: " + clipRunes(messages[0].Content, maxPriorSummaryRunes) + "\n"
		start = 1
	}
	// Collect each rendered entry (role header plus its multi-line content)
	// as one element, newest-first under the size budget, then reverse the
	// slice to restore chronological order. Reversing whole entries — not
	// raw lines — keeps a message's internal line order intact.
	var entries []string
	clipped := false
	size := 0
	for i := len(messages) - 1; i >= start; i-- {
		m := messages[i]
		entry := m.Role
		if m.ToolName != "" {
			entry += "(" + m.ToolName + ")"
		}
		entry += ": " + clipRunes(m.Content, maxPerMessageRunes)
		if len(m.ToolCalls) > 0 {
			if raw, err := json.Marshal(m.ToolCalls); err == nil {
				entry += " [tool_calls: " + string(raw) + "]"
			}
		}
		entry += "\n"
		if size+len(entry) > maxSummaryChars {
			clipped = true
			break
		}
		size += len(entry)
		entries = append(entries, entry)
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	body := strings.Join(entries, "")
	if !clipped {
		return prefix + body
	}
	return prefix + "[earliest history omitted for size; summarizing the most recent part]\n" + body
}

// EstimateTokens exposes the session's heuristic token estimator so metrics
// and budgeting code can price prompts with the same ruler compaction uses.
func EstimateTokens(text string) int {
	return estimateTextTokens(text)
}

func estimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	cjk := 0
	for _, r := range text {
		if (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3040 && r <= 0x30FF) || (r >= 0xAC00 && r <= 0xD7AF) {
			cjk++
		}
	}
	asciiApprox := len(text) / 4
	return cjk + asciiApprox
}

// estimateMessageTokens adds the vision proxy cost for attached images so
// compaction triggers and /tokens account for pictures, not just text.
func estimateMessageTokens(content string) int {
	return estimateTextTokens(content) + vision.CountMarkers(content)*vision.EstimatedTokensPerImage
}
