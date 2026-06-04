package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/session"
)

func resolveSessionID(c *Ctx, raw string) (string, error) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return "", fmt.Errorf("session id is empty")
	}
	infos, err := session.ListSessions(c.Cfg)
	if err != nil {
		return "", err
	}
	if len(infos) == 0 {
		return "", fmt.Errorf("no saved sessions found; run /sessions")
	}
	for _, info := range infos {
		if info.ID == target {
			return info.ID, nil
		}
	}
	matches := make([]string, 0, 4)
	for _, info := range infos {
		if strings.HasPrefix(info.ID, target) {
			matches = append(matches, info.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("session %q not found; run /sessions to list valid ids", target)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("session prefix %q is ambiguous (%s); provide more characters", target, strings.Join(matches, ", "))
	}
}

func lastAssistantResponse(msgs []session.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if content == "" || strings.HasPrefix(content, "[runtime]") {
			continue
		}
		return content
	}
	return ""
}

func trimForDisplay(s string, maxChars int) string {
	text := strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if text == "" || maxChars <= 0 {
		return ""
	}
	if len(text) <= maxChars {
		return text
	}
	return text[:maxChars] + "..."
}
