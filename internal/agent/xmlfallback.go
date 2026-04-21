package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	openInvokeRe   = regexp.MustCompile(`(?is)<invoke[^>]*name=["']([A-Za-z0-9_:-]+)["'][^>]*>`)
	openToolCallRe = regexp.MustCompile(`(?is)<tool_call[^>]*name=["']([A-Za-z0-9_:-]+)["'][^>]*>`)
	toolTagOpenRe  = regexp.MustCompile(`(?s)<([A-Z][A-Z0-9_]*)>`)
)

// parseXMLFallback recovers a tool call from a raw model reply that did not use
// the native tool-call API. It accepts three envelopes:
//
//	<invoke name="ToolName">{...}</invoke>
//	<tool_call name="ToolName">{...}</tool_call>
//	<TOOLNAME>{...}</TOOLNAME>
//
// The JSON body is extracted with a brace-balanced, string-aware scanner so it
// survives nested braces inside string values (e.g. glob patterns like
// "**/*.{gd,cs}") and tolerates trailing junk between the closing brace and the
// closing tag (e.g. an extra quote the model accidentally appended).
func parseXMLFallback(text string) (string, map[string]any, bool) {
	clean := strings.TrimSpace(text)
	if !strings.Contains(clean, "</") {
		return "", nil, false
	}

	if name, params, ok := parseNamedEnvelope(clean, openInvokeRe, "</invoke>"); ok {
		return name, params, true
	}
	if name, params, ok := parseNamedEnvelope(clean, openToolCallRe, "</tool_call>"); ok {
		return name, params, true
	}
	if name, params, ok := parseToolTagEnvelope(clean); ok {
		return name, params, true
	}
	return "", nil, false
}

func parseNamedEnvelope(s string, openRe *regexp.Regexp, closeTag string) (string, map[string]any, bool) {
	loc := openRe.FindStringSubmatchIndex(s)
	if loc == nil {
		return "", nil, false
	}
	name := strings.TrimSpace(s[loc[2]:loc[3]])
	body, end, ok := extractBalancedJSON(s, loc[1])
	if !ok {
		return "", nil, false
	}
	if !containsFold(s[end:], closeTag) {
		return "", nil, false
	}
	params, ok := decodeToolJSON(body)
	if !ok {
		return "", nil, false
	}
	return name, params, true
}

func parseToolTagEnvelope(s string) (string, map[string]any, bool) {
	loc := toolTagOpenRe.FindStringSubmatchIndex(s)
	if loc == nil {
		return "", nil, false
	}
	tag := s[loc[2]:loc[3]]
	body, end, ok := extractBalancedJSON(s, loc[1])
	if !ok {
		return "", nil, false
	}
	closeTag := "</" + tag + ">"
	if !strings.Contains(s[end:], closeTag) {
		return "", nil, false
	}
	params, ok := decodeToolJSON(body)
	if !ok {
		return "", nil, false
	}
	return toToolName(tag), params, true
}

// extractBalancedJSON scans s starting at start, skips leading whitespace, and
// returns the first balanced JSON object as a substring along with the index
// just past the closing brace. It tracks string boundaries (with backslash
// escapes) so braces inside string values are ignored.
func extractBalancedJSON(s string, start int) (string, int, bool) {
	i := start
	for i < len(s) && isJSONSpace(s[i]) {
		i++
	}
	if i >= len(s) || s[i] != '{' {
		return "", i, false
	}
	depth := 0
	inString := false
	escaped := false
	for j := i; j < len(s); j++ {
		c := s[j]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[i : j+1], j + 1, true
			}
		}
	}
	return "", len(s), false
}

func decodeToolJSON(raw string) (map[string]any, bool) {
	params := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &params); err == nil {
		return params, true
	}
	fixed := fixTrailingCommas(raw)
	if err := json.Unmarshal([]byte(fixed), &params); err == nil {
		return params, true
	}
	return nil, false
}

func toToolName(raw string) string {
	parts := strings.Split(strings.ToLower(raw), "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}

func fixTrailingCommas(in string) string {
	re := regexp.MustCompile(`,\s*([}\]])`)
	return re.ReplaceAllString(in, "$1")
}

func isJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
