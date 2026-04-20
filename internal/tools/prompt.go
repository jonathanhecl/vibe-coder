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
	b.WriteString("When to call a tool (be proactive):\n")
	b.WriteString("- The user asks about \"the project\", \"this code\", \"my repo\", or any\n")
	b.WriteString("  other workspace content -> use Glob/Grep to map the repo, then Read\n")
	b.WriteString("  the most relevant files. Never ask the user to paste code.\n")
	b.WriteString("- The user asks what a specific file or function does -> Read that file\n")
	b.WriteString("  first; only after reading it may you summarize.\n")
	b.WriteString("- The user asks to find something -> Grep for the symbol or string.\n")
	b.WriteString("- A previous tool result is incomplete -> issue another tool call to\n")
	b.WriteString("  gather the missing information.\n")
	b.WriteString("- Only skip tool calls when the answer is purely conceptual and does\n")
	b.WriteString("  not depend on workspace contents.\n\n")
	b.WriteString("When to plan with TodoWrite:\n")
	b.WriteString("- Before starting any task that needs 3+ distinct steps (multi-file edits,\n")
	b.WriteString("  refactors, bug hunts, or anything where the user benefits from seeing\n")
	b.WriteString("  the plan), call TodoWrite once with the full list and statuses.\n")
	b.WriteString("- Mark exactly one item as \"in_progress\" at a time. After completing a\n")
	b.WriteString("  step, call TodoWrite again with merge=true to flip it to \"completed\"\n")
	b.WriteString("  and the next one to \"in_progress\". Add new items as they appear.\n")
	b.WriteString("- Skip TodoWrite for trivial single-step tasks (one Read, one Bash, a\n")
	b.WriteString("  short answer); the panel only adds noise there.\n\n")
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
