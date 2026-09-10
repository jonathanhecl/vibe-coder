package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

const (
	compactionTimeout  = 90 * time.Second
	compactionKeepTail = 30
)

// compactionSummaryPrompt instructs the sidecar to preserve the information
// the agent needs to continue: the task, decisions, file paths, TODO state,
// and what remains to do. A generic "summarize concisely" loses exactly the
// details that cause the agent to repeat work or forget the goal.
const compactionSummaryPrompt = `Summarize the earlier portion of this agent conversation. Preserve:
- The user's original task and any constraints or preferences they stated.
- Key decisions made and the reasoning behind them.
- File paths that were read, created, or modified (with one-line descriptions).
- Commands that were run and their outcome (success/failure, key findings).
- The current TODO state: which steps are done and which remain.
- Any errors or blockers encountered and how they were resolved.
Do not include verbatim file contents or full command output. Be concise but complete.`

func (s *Session) Compact(ctx context.Context, force bool) error {
	if s == nil {
		return nil
	}

	s.mu.RLock()
	cfg := s.cfg
	client := s.client
	if cfg == nil || len(s.messages) <= compactionKeepTail {
		s.mu.RUnlock()
		return nil
	}
	if !force && len(s.messages) <= 300 && s.tokenEstimate <= int(0.7*float64(cfg.ContextWindow)) {
		s.mu.RUnlock()
		return nil
	}
	cut := len(s.messages) - compactionKeepTail
	old := append([]Message(nil), s.messages[:cut]...)
	recent := append([]Message(nil), s.messages[cut:]...)
	revision := s.revision
	s.mu.RUnlock()

	var summary string
	if client != nil && cfg.SidecarInUse() {
		compactCtx, cancel := context.WithTimeout(ctx, compactionTimeout)
		resp, err := client.ChatSync(compactCtx, ollama.ChatRequest{
			Model: cfg.SidecarModel,
			Messages: []ollama.Message{
				{Role: "system", Content: compactionSummaryPrompt},
				{Role: "user", Content: renderMessagesForSummary(old)},
			},
			Stream: false,
		})
		cancel()
		if err == nil && strings.TrimSpace(resp.Content) != "" {
			summary = resp.Content
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
	// model).
	newMsgs := make([]Message, 0, len(recent)+3)
	newMsgs = append(newMsgs, Message{
		Role:      "user",
		Content:   "[Earlier conversation summary]\n" + summary,
		Timestamp: time.Now().UTC(),
	})
	if first := s.firstUserMessageUnlocked(); first != nil {
		newMsgs = append(newMsgs, *first)
	}
	newMsgs = append(newMsgs, normalizeKeptMessages(recent)...)
	s.messages = newMsgs
	s.recomputeTokenEstimate()
	s.revision++
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

func renderMessagesForSummary(messages []Message) string {
	// Bound the sidecar input: at the compaction threshold the old
	// transcript can hold megabytes (verbatim tool outputs), which would
	// stall the small sidecar model past its timeout and degrade to the
	// static truncation note. Keep the most recent content so the summary
	// stays continuous with the messages kept verbatim.
	const maxSummaryChars = 48 * 1024
	var b strings.Builder
	clipped := false
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		line := m.Role
		if m.ToolName != "" {
			line += "(" + m.ToolName + ")"
		}
		line += ": " + m.Content
		if len(m.ToolCalls) > 0 {
			if raw, err := json.Marshal(m.ToolCalls); err == nil {
				line += " [tool_calls: " + string(raw) + "]"
			}
		}
		line += "\n"
		if b.Len()+len(line) > maxSummaryChars {
			clipped = true
			break
		}
		b.WriteString(line)
	}
	out := b.String()
	if !clipped {
		// Built newest-first above; restore chronological order.
		return reverseLines(out)
	}
	return "[earliest history omitted for size; summarizing the most recent part]\n" + reverseLines(out)
}

// reverseLines flips a "\n"-terminated line block end to start.
func reverseLines(s string) string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
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
