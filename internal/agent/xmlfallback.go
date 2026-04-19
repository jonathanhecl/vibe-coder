package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	invokePattern   = regexp.MustCompile(`(?is)<invoke[^>]*name=["']([A-Za-z0-9_:-]+)["'][^>]*>\s*(\{.*?\})\s*</invoke>`)
	toolTagPattern  = regexp.MustCompile(`(?is)<([A-Z][A-Z0-9_]*)>\s*(\{.*?\})\s*</([A-Z][A-Z0-9_]*)>`)
	toolCallPattern = regexp.MustCompile(`(?is)<tool_call[^>]*name=["']([A-Za-z0-9_:-]+)["'][^>]*>\s*(\{.*?\})\s*</tool_call>`)
)

func parseXMLFallback(text string) (string, map[string]any, bool) {
	clean := strings.TrimSpace(text)
	if !strings.Contains(clean, "</") {
		return "", nil, false
	}

	for _, pattern := range []*regexp.Regexp{invokePattern, toolCallPattern, toolTagPattern} {
		m := pattern.FindStringSubmatch(clean)
		expectedLen := 3
		if pattern == toolTagPattern {
			expectedLen = 4
		}
		if len(m) != expectedLen {
			continue
		}
		name := strings.TrimSpace(m[1])
		if pattern == toolTagPattern {
			closeName := strings.TrimSpace(m[3])
			if !strings.EqualFold(name, closeName) {
				continue
			}
		}
		if pattern == toolTagPattern {
			name = toToolName(name)
		}
		rawJSON := strings.TrimSpace(m[2])
		params := map[string]any{}
		if err := json.Unmarshal([]byte(rawJSON), &params); err != nil {
			fixed := fixTrailingCommas(rawJSON)
			if err := json.Unmarshal([]byte(fixed), &params); err != nil {
				return "", nil, false
			}
		}
		return name, params, true
	}
	return "", nil, false
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
