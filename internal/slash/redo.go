package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// runRedoCommand re-prints the last assistant response from the session so the
// user can recover the context of what they were working on, rendered exactly
// like the live answer (including whitespace and markdown structure).
func runRedoCommand(c *Ctx) error {
	if c.Session == nil {
		fmt.Fprintln(c.Out, "No active session.")
		return nil
	}
	msgs := c.Session.MessagesReadOnly()
	last := lastAssistantResponse(msgs)
	if strings.TrimSpace(last) == "" {
		fmt.Fprintln(c.Out, "No previous assistant response in this session.")
		return nil
	}

	st := tui.NewStyle(c.Out)
	if prompt := lastUserPrompt(msgs); prompt != "" {
		fmt.Fprintf(c.Out, "%s %s\n", st.BoldCyan("user >"), st.Dim(trimForDisplay(prompt, 400)))
	}
	fmt.Fprintf(c.Out, "%s ", st.BoldGreen("assistant >"))
	md := tui.NewMarkdownRenderer(st)
	md.Write(c.Out, last)
	md.Flush(c.Out)
	return nil
}

// lastUserPrompt returns the most recent real user prompt, skipping runtime
// notes and tool observations that share the user role.
func lastUserPrompt(msgs []session.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != "user" {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if content == "" ||
			strings.HasPrefix(content, "[runtime]") ||
			strings.HasPrefix(content, "[System Note]") ||
			strings.HasPrefix(content, "[AUTO-TEST]") {
			continue
		}
		return content
	}
	return ""
}
