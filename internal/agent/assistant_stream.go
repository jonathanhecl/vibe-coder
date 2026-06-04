package agent

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// toolEnvelopeScanOverlap is how many bytes before the previous buffer end we
// rescan for a tool envelope start so a tag split across stream chunks is not
// missed. Must be >= len("<tool_call")-1 and cover UTF-8 rune after '<'.
const toolEnvelopeScanOverlap = 32

// toolEnvelopeByteIndex returns the byte index where tool XML starts, or -1 if none yet.
// Matches <invoke, <tool_call, and <ToolName> (leading uppercase ASCII letter after '<').
func toolEnvelopeByteIndex(s string) int {
	best := -1
	n := len(s)
	for i := 0; i < n; i++ {
		if s[i] != '<' {
			continue
		}
		// <invoke (case-insensitive)
		if i+7 <= n && asciiPrefixFold(s[i+1:], "invoke") {
			if best < 0 || i < best {
				best = i
			}
			continue
		}
		// <tool_call (case-insensitive)
		if i+10 <= n && asciiPrefixFold(s[i+1:], "tool_call") {
			if best < 0 || i < best {
				best = i
			}
			continue
		}
		// <ToolName> style (not </...)
		if i+1 < n && s[i+1] != '/' {
			r, _ := utf8.DecodeRuneInString(s[i+1:])
			if r != utf8.RuneError && unicode.IsUpper(r) {
				if best < 0 || i < best {
					best = i
				}
			}
		}
	}
	return best
}

// HasPotentialToolStart returns the byte index of the last '<' if it starts
// a sequence that matches a prefix of "<invoke" or "<tool_call" (case-insensitive).
func HasPotentialToolStart(s string) (int, bool) {
	idx := strings.LastIndexByte(s, '<')
	if idx < 0 {
		return -1, false
	}
	suffix := s[idx:]
	low := strings.ToLower(suffix)
	if strings.HasPrefix("<invoke", low) || strings.HasPrefix("<tool_call", low) {
		return idx, true
	}
	return -1, false
}


// asciiPrefixFold reports whether p starts with prefixLower (ASCII letters,
// all lowercase in prefixLower) under ASCII case folding.
func asciiPrefixFold(p, prefixLower string) bool {
	if len(p) < len(prefixLower) {
		return false
	}
	for j := 0; j < len(prefixLower); j++ {
		c := p[j]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != prefixLower[j] {
			return false
		}
	}
	return true
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
