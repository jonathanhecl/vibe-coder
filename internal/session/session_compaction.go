package session

import (
	"context"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

func (s *Session) Compact(ctx context.Context, force bool) error {
	if !force && !s.ShouldCompact() {
		return nil
	}
	if len(s.messages) <= 30 {
		return nil
	}
	cut := len(s.messages) - 30
	old := append([]Message(nil), s.messages[:cut]...)
	recent := append([]Message(nil), s.messages[cut:]...)

	var summary string
	if s.client != nil && s.cfg.SidecarInUse() {
		text := renderMessagesForSummary(old)
		resp, err := s.client.ChatSync(ctx, ollama.ChatRequest{
			Model: s.cfg.SidecarModel,
			Messages: []ollama.Message{
				{Role: "system", Content: "Summarize the conversation concisely."},
				{Role: "user", Content: text},
			},
			Stream: false,
		})
		if err == nil && strings.TrimSpace(resp.Content) != "" {
			summary = resp.Content
		}
	}
	if summary == "" {
		summary = "Earlier conversation truncated to stay within context limits."
	}
	s.sessAddSummary(summary)
	s.messages = append(s.messages, recent...)
	// Avoid starting with tool-like or empty roles in future extensions.
	for len(s.messages) > 0 && strings.TrimSpace(s.messages[0].Role) == "" {
		s.messages = s.messages[1:]
	}
	s.recomputeTokenEstimate()
	return nil
}

func (s *Session) sessAddSummary(summary string) {
	s.messages = []Message{{
		Role:      "user",
		Content:   "[Earlier conversation summary]\n" + summary,
		Timestamp: time.Now().UTC(),
	}}
	s.recomputeTokenEstimate()
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
