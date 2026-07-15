package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
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
		total += estimateTextTokens(msg.Content)
	}
	s.tokenEstimate = total
}

func renderMessagesForSummary(messages []Message) string {
	var b strings.Builder
	for _, m := range messages {
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
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
