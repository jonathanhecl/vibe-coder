package agent

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// toolEnvelopeByteIndex returns the byte index where tool XML starts, or -1 if none yet.
// Matches <invoke, <tool_call, and <ToolName> (leading uppercase ASCII letter after '<').
func toolEnvelopeByteIndex(s string) int {
	best := -1
	low := strings.ToLower(s)
	if i := strings.Index(low, "<invoke"); i >= 0 {
		best = i
	}
	if i := strings.Index(low, "<tool_call"); i >= 0 && (best < 0 || i < best) {
		best = i
	}
	for i := 0; i+1 < len(s); i++ {
		if s[i] != '<' || s[i+1] == '/' {
			continue
		}
		r, _ := utf8.DecodeRuneInString(s[i+1:])
		if r == utf8.RuneError || !unicode.IsUpper(r) {
			continue
		}
		if best < 0 || i < best {
			best = i
		}
	}
	return best
}

// assistantTextAfterFirstClosedTool returns prose after the first complete tool envelope
// (e.g. text after </invoke>) so it can be shown even though the XML was hidden while streaming.
func assistantTextAfterFirstClosedTool(s string) string {
	t := s
	low := strings.ToLower(t)
	if idx := strings.Index(low, "<invoke"); idx >= 0 {
		rel := strings.Index(low[idx:], "</invoke>")
		if rel < 0 {
			return ""
		}
		after := t[idx+rel+len("</invoke>"):]
		return strings.TrimLeftFunc(after, unicode.IsSpace)
	}
	if idx := strings.Index(low, "<tool_call"); idx >= 0 {
		rel := strings.Index(low[idx:], "</tool_call>")
		if rel < 0 {
			return ""
		}
		after := t[idx+rel+len("</tool_call>"):]
		return strings.TrimLeftFunc(after, unicode.IsSpace)
	}
	loc := toolTagOpenRe.FindStringSubmatchIndex(t)
	if loc == nil {
		return ""
	}
	tag := t[loc[2]:loc[3]]
	close := "</" + tag + ">"
	off := loc[1]
	relClose := strings.Index(t[off:], close)
	if relClose < 0 {
		return ""
	}
	after := t[off+relClose+len(close):]
	return strings.TrimLeftFunc(after, unicode.IsSpace)
}

// assistantVisibleText strips hidden reasoning blocks from a raw model reply
// and returns only user-visible prose. Tool envelopes are ignored because they
// are not visible assistant text either.
func assistantVisibleText(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return ""
	}
	for {
		next, changed := stripFirstThinkBlock(t)
		if !changed {
			break
		}
		t = next
	}
	if idx := toolEnvelopeByteIndex(t); idx >= 0 {
		t = t[:idx]
	}
	return strings.TrimSpace(t)
}

func stripFirstThinkBlock(s string) (string, bool) {
	low := strings.ToLower(s)
	pairs := [][2]string{
		{"<think>", "</think>"},
		{"<thinking>", "</thinking>"},
	}
	bestOpen := -1
	openTag := ""
	closeTag := ""
	for _, p := range pairs {
		if i := strings.Index(low, p[0]); i >= 0 && (bestOpen < 0 || i < bestOpen) {
			bestOpen = i
			openTag = p[0]
			closeTag = p[1]
		}
	}
	if bestOpen < 0 {
		return s, false
	}
	afterOpen := bestOpen + len(openTag)
	relClose := strings.Index(low[afterOpen:], closeTag)
	if relClose < 0 {
		// Drop dangling think block to avoid "thinking-only" ghost replies.
		return strings.TrimSpace(s[:bestOpen]), true
	}
	closeStart := afterOpen + relClose
	closeEnd := closeStart + len(closeTag)
	merged := s[:bestOpen] + s[closeEnd:]
	return strings.TrimSpace(merged), true
}
