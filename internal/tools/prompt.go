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
	b.WriteString("- Read/Write/Edit file_path must be a real absolute path on disk (under the\n")
	b.WriteString("  workspace). Prefer paths you have seen from Glob or earlier tools.\n")
	b.WriteString("- If a tool returns a message starting with \"PATH ERROR:\", the path was wrong or\n")
	b.WriteString("  unreachable; fix the path (Glob, verify spelling) and retry — do not repeat the\n")
	b.WriteString("  same broken path.\n")
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
	b.WriteString("Internet access (you DO have it):\n")
	b.WriteString("- The WebSearch tool gives you live results from DuckDuckGo, and Bash\n")
	b.WriteString("  can run curl/wget. So you ARE connected to the internet through\n")
	b.WriteString("  these tools — never tell the user \"I have no internet access\" or\n")
	b.WriteString("  \"I cannot fetch real-time data\".\n")
	b.WriteString("- For anything time-sensitive (weather, prices, news, current events,\n")
	b.WriteString("  package versions, docs of a library you are unsure about, sports\n")
	b.WriteString("  results, exchange rates, etc.) call WebSearch first and answer\n")
	b.WriteString("  based on the results. Cite the source URL when relevant.\n")
	b.WriteString("- If WebSearch returns nothing useful, say so explicitly and offer\n")
	b.WriteString("  to retry with a different query — do not fall back to \"I cannot\n")
	b.WriteString("  access the internet\".\n\n")
	b.WriteString("When to plan with TodoWrite:\n")
	b.WriteString("- Before starting any task that needs 3+ distinct steps (multi-file edits,\n")
	b.WriteString("  refactors, bug hunts, or anything where the user benefits from seeing\n")
	b.WriteString("  the plan), call TodoWrite once with the full list and statuses.\n")
	b.WriteString("- Mark exactly one item as \"in_progress\" at a time. After completing a\n")
	b.WriteString("  step, call TodoWrite again with merge=true to flip it to \"completed\"\n")
	b.WriteString("  and the next one to \"in_progress\". Add new items as they appear.\n")
	b.WriteString("- Skip TodoWrite for trivial single-step tasks (one Read, one Bash, a\n")
	b.WriteString("  short answer); the panel only adds noise there.\n\n")
	b.WriteString("AskUserQuestion (multiple choice in the terminal):\n")
	b.WriteString("- \"questions\" must be an array of objects, each with \"prompt\" and \"options\".\n")
	b.WriteString("- \"options\" is a non-empty array: either strings [\"A\",\"B\"] or objects with \"label\".\n")
	b.WriteString("- Do not send {\"questions\":[\"text?\",\"more?\"]} — that shape is invalid.\n\n")
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
