package tui

import (
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// capabilityTagColors maps every model capability to a stable xterm-256 color,
// so the same tag looks identical in /models, /setmodel and onboarding. The
// palette groups by meaning: cool tones for perception (vision/thinking),
// warm tones for action (tools/decision), and neutral for the base completion
// tag.
var capabilityTagColors = map[string]int{
	config.CapabilityCompletion: 250, // light gray
	config.CapabilityTools:      178, // amber / gold
	config.CapabilityThinking:   141, // violet
	config.CapabilityVision:     45,  // cyan
	config.CapabilityEmbedding:  205, // pink
	config.CapabilityDecision:   208, // orange
}

// CapabilityTag renders one capability tag with its stable color. Tags that are
// not model capabilities (such as "recommended" or free-form notes) fall back
// to gray so they stay muted rather than borrowing a capability color.
func (s Style) CapabilityTag(tag string) string {
	if code, ok := capabilityTagColors[strings.ToLower(strings.TrimSpace(tag))]; ok {
		return s.Color256(code, tag)
	}
	return s.Gray(tag)
}

// CapabilityTags renders a slice of tags with their own colors, ready to be
// joined by a caller-chosen separator.
func (s Style) CapabilityTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		out = append(out, s.CapabilityTag(tag))
	}
	return out
}
