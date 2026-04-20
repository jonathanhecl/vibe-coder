package tools

import (
	"fmt"
	"sort"
	"strings"
)

// RenderPromptBlock builds a system-prompt section that lists available tools
// and explains the exact invocation format the agent will parse.
//
// Models without native tool-call support need this to know the tools exist
// and the XML envelope expected by parseXMLFallback.
func RenderPromptBlock(reg *Registry) string {
	if reg == nil {
		return ""
	}
	names := reg.Names()
	if len(names) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Tools\n")
	b.WriteString("You can call tools to inspect files, run commands, and search the web.\n")
	b.WriteString("To call a tool, reply with a single XML block in this exact format:\n\n")
	b.WriteString("<invoke name=\"ToolName\">{\"param\":\"value\"}</invoke>\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Emit only one tool call per reply, with no extra prose around it.\n")
	b.WriteString("- The body MUST be valid JSON for that tool's parameters.\n")
	b.WriteString("- Wait for the tool result before deciding the next step.\n")
	b.WriteString("- If no tool is needed, answer directly in plain text.\n\n")
	b.WriteString("Available tools:\n")
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
			fmt.Fprintf(&b, "- %s(%s): %s\n", name, params, desc)
		} else {
			fmt.Fprintf(&b, "- %s: %s\n", name, desc)
		}
	}
	return strings.TrimRight(b.String(), "\n")
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
