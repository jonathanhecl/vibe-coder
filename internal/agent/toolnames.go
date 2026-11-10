package agent

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// maxUnknownToolRounds caps how many consecutive turns in one Run may consist
// solely of tool calls the registry does not know. Each round already tells
// the model exactly which tools exist, so a model that keeps inventing names
// is stuck; the cap turns that into a reported stop instead of silent churn
// until the iteration budget runs out.
const maxUnknownToolRounds = 3

// normalizeToolName folds a tool name for comparison: lowercase with the
// separator characters models invent (_ - space .) dropped, so "todo_write",
// "Todo-Write" and "Todo Write" all compare equal to "TodoWrite".
func normalizeToolName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range strings.ToLower(name) {
		switch r {
		case '_', '-', ' ', '.':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// resolveToolName maps a model-supplied tool name onto the registry's
// canonical spelling. Exact matches win; then a leading "functions." or
// "tools." namespace prefix (OpenAI-style hallucinations) is stripped, and
// finally the normalized form is compared against every registered name so
// case and separator drift ("read", "web-fetch", "todo_write") still lands
// on the real tool. Names with no match — including near-misses like
// "read_file" — report ok=false so the caller can answer with an error
// instead of silently dropping the call.
func (a *Agent) resolveToolName(name string) (string, bool) {
	if a == nil || a.reg == nil {
		return "", false
	}
	if a.reg.Get(name) != nil {
		return name, true
	}
	candidate := name
	lower := strings.ToLower(candidate)
	for _, prefix := range []string{"functions.", "tools."} {
		if strings.HasPrefix(lower, prefix) {
			candidate = candidate[len(prefix):]
			break
		}
	}
	if a.reg.Get(candidate) != nil {
		return candidate, true
	}
	want := normalizeToolName(candidate)
	if want == "" {
		return "", false
	}
	for _, regName := range a.reg.Names() {
		if normalizeToolName(regName) == want {
			return regName, true
		}
	}
	return "", false
}

// availableToolNames lists the registry tools the active mode permits, in
// sorted order — the same set nativeChatTools advertises on the wire, so an
// unknown-tool error can point the model at names it could actually call.
func (a *Agent) availableToolNames() []string {
	if a == nil || a.reg == nil {
		return nil
	}
	planMode := a.InPlanMode()
	reviewMode := a.InReviewMode()
	offline := a.isOffline()
	var out []string
	for _, name := range a.reg.Names() {
		if tools.IsAllowedInMode(a.reg.Get(name), planMode, reviewMode, offline) {
			out = append(out, name)
		}
	}
	return out
}

// unknownToolMessage is the tool result recorded for a call the registry
// cannot resolve: it names the missing tool and lists what does exist.
func (a *Agent) unknownToolMessage(name string) string {
	if names := a.availableToolNames(); len(names) > 0 {
		return fmt.Sprintf("Error: tool %q does not exist. Use one of the available tools: %s.", name, strings.Join(names, ", "))
	}
	return fmt.Sprintf("Error: tool %q does not exist.", name)
}
