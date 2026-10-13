package tools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/prompt"
)

// RenderGuidelinesBlock renders the shared tool-usage guidelines for the
// system prompt. It applies to both tool modes: models with native function
// calling and models that use the XML fallback envelope. It returns "" when
// the registry is nil or empty.
func RenderGuidelinesBlock(reg *Registry, offline bool) string {
	if reg == nil || len(reg.Names()) == 0 {
		return ""
	}
	return prompt.Render("tool_guidelines", struct{ Offline bool }{Offline: offline})
}

// RenderXMLProtocolBlock renders the XML invocation protocol plus the tool
// catalogue for models without native tool-call support (the envelope the
// agent's parseXMLFallback parses). It returns "" when the registry is nil
// or empty.
func RenderXMLProtocolBlock(reg *Registry) string {
	if reg == nil {
		return ""
	}
	names := reg.Names()
	if len(names) == 0 {
		return ""
	}
	lines := make([]string, 0, len(names))
	for _, name := range names {
		t := reg.Get(name)
		if t == nil {
			continue
		}
		desc := strings.TrimSpace(t.Description())
		if desc == "" {
			desc = "(no description)"
		}
		params := summarizeParams(t.Schema())
		if params != "" {
			lines = append(lines, fmt.Sprintf("%s(%s): %s", name, params, desc))
		} else {
			lines = append(lines, fmt.Sprintf("%s: %s", name, desc))
		}
	}
	return prompt.Render("tool_protocol_xml", struct{ Tools []string }{Tools: lines})
}

func summarizeParams(schema Schema) string {
	props, _ := schema.Function.Parameters["properties"].(map[string]any)
	if len(props) == 0 {
		return ""
	}
	required := map[string]bool{}
	if list, ok := schema.Function.Parameters["required"].([]any); ok {
		for _, item := range list {
			if name, ok := item.(string); ok {
				required[name] = true
			}
		}
	}
	keys := make([]string, 0, len(props))
	for name := range props {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, name := range keys {
		if required[name] {
			parts = append(parts, name)
		} else {
			parts = append(parts, name+"?")
		}
	}
	return strings.Join(parts, ", ")
}
