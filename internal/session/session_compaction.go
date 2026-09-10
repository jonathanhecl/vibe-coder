package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

const compactionTimeout = 90 * time.Second

func (s *Session) Compact(ctx context.Context, force bool) error {
	if s == nil {
		return nil
	}

	s.mu.RLock()
	cfg := s.cfg
	client := s.client
	if cfg == nil || (!force && (len(s.messages) <= 30 || (len(s.messages) <= 300 && s.tokenEstimate <= int(0.7*float64(cfg.ContextWindow))))) || len(s.messages) <= 30 {
		s.mu.RUnlock()
		return nil
	}
	cut := len(s.messages) - 30
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
				{Role: "system", Content: "Summarize the conversation concisely."},
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
	s.messages = append([]Message{{
		Role:      "user",
		Content:   "[Earlier conversation summary]\n" + summary,
		Timestamp: time.Now().UTC(),
	}}, recent...)
	s.recomputeTokenEstimate()
	s.revision++
	return nil
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
		line := m.Role + ": " + m.Content + "\n"
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
