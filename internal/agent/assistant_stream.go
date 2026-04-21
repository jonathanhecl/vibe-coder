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
