package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// toolEnvelopeRE matches <invoke...>...</invoke>, <tool_call...>...</tool_call>,
// and <TOOLNAME>...</TOOLNAME> style envelopes so they never leak into the
// visible assistant stream.
var toolEnvelopeRE = regexp.MustCompile(`(?is)<invoke\b[^>]*>.*?</invoke>|<tool_call\b[^>]*>.*?</tool_call>|<[A-Z][A-Z0-9_]*>.*?</[A-Z][A-Z0-9_]*>`)

func stripToolEnvelopes(s string) string { return toolEnvelopeRE.ReplaceAllString(s, "") }

// StreamAssistant prints assistant tokens as they arrive. It strips
// <thinking>...</thinking> blocks from the visible reply and re-routes them to a
// dimmed thinking section so they read like Cursor's reasoning panel.
// It also strips tool-call XML envelopes so the user never sees raw <invoke>
// tags even if the upstream streaming filter misses an edge case.
func (u *PlainUI) StreamAssistant(text string) {
	u.stopSpinner()
	u.mu.Lock()
	defer u.mu.Unlock()

	u.flushPendingToolLocked()

	if !u.streamingAssistant {
		if u.style.Enabled() {
			icon := u.style.BrightGreen(iconAssistant)
			label := u.style.BoldGreen("assistant")
			if u.planMode {
				icon = u.style.Yellow(iconAssistant)
				label = u.style.BoldYellow("assistant")
			}
			fmt.Fprintf(u.out, "%s %s > ",
				icon,
				label,
			)
		}
		u.streamingAssistant = true
		u.assistantReplyStart = time.Now()
		u.assistantHadVisible = false
		u.assistantLines = 0
		u.ensureMarkdownLocked()
	}

	u.streamBuffer.WriteString(text)
	for {
		buf := u.streamBuffer.String()
		visible, thinking, leftover, hasMore := splitThinking(buf)
		visible = stripToolEnvelopes(visible)

		if visible != "" {
			u.ensureMarkdownLocked()
			u.markdown.Write(u.out, visible)
			u.assistantHadVisible = true
			u.assistantLines += strings.Count(visible, "\n")
		}
		if thinking != "" {
			u.writeThinkingChunkLocked(thinking)
		}
		if !hasMore {
			u.streamBuffer.Reset()
			u.streamBuffer.WriteString(leftover)
			return
		}
		u.streamBuffer.Reset()
		u.streamBuffer.WriteString(leftover)
	}
}

// EndAssistant marks the end of an assistant turn: drains the markdown
// buffer (Flush already ends the last line with a newline). We intentionally
// do not emit an extra blank line here — that used to double up with Flush
// and left an empty row before the next line (e.g. a tool call).
func (u *PlainUI) EndAssistant() {
	u.stopSpinner()
	u.mu.Lock()
	defer u.mu.Unlock()

	if rest := u.streamBuffer.String(); rest != "" {
		rest = stripToolEnvelopes(rest)
		if rest != "" {
			u.ensureMarkdownLocked()
			u.markdown.Write(u.out, rest)
			u.assistantHadVisible = true
			u.assistantLines += strings.Count(rest, "\n")
		}
		u.streamBuffer.Reset()
	}
	if u.markdown != nil {
		u.markdown.Flush(u.out)
		u.markdown.Reset()
	}
	u.closeThinkingLocked(true)
	if u.streamingAssistant && u.assistantHadVisible && !u.assistantReplyStart.IsZero() {
		elapsed := formatElapsed(time.Since(u.assistantReplyStart))
		if u.style.Enabled() {
			fmt.Fprintf(u.out, "\n%s %s\n",
				u.style.Dim(iconRule),
				u.style.Dim("responded in "+elapsed),
			)
		} else {
			fmt.Fprintf(u.out, "\nresponded in %s\n", elapsed)
		}
		u.assistantLines += 2
	}
	// If the turn produced no visible prose (only a tool call), erase the
	// assistant label line so the user sees the tool card directly.
	if u.streamingAssistant && !u.assistantHadVisible {
		fmt.Fprint(u.out, "\r\x1b[2K")
	}
	u.assistantReplyStart = time.Time{}
	u.assistantHadVisible = false
	if u.streamingAssistant {
		u.streamingAssistant = false
	}
}

// CollapseAssistantOutput erases the assistant turn from the terminal so the
// user sees only the tool card, matching Cursor's behaviour when a model
// replies with a tool call and no substantive prose.
func (u *PlainUI) CollapseAssistantOutput() {
	u.stopSpinner()
	u.mu.Lock()
	defer u.mu.Unlock()

	if !u.style.Enabled() || u.assistantLines == 0 {
		u.assistantLines = 0
		return
	}

	lines := u.assistantLines
	u.assistantLines = 0
	// Move cursor up N lines and clear to the end of the screen.
	fmt.Fprintf(u.out, "\x1b[%dA\x1b[J", lines)
}

// StreamThinking renders native Ollama "thinking" tokens as a dim, indented
// panel under the assistant bubble, similar to Cursor's reasoning panel.
// Each chunk is emitted live so the user can see the model reasoning in
// real time, then EndThinking closes the panel before the final answer.
func (u *PlainUI) StreamThinking(text string) {
	if text == "" {
		return
	}
	u.stopSpinner()
	u.mu.Lock()
	defer u.mu.Unlock()

	u.flushPendingToolLocked()
	u.endAssistantLineLocked()
	if !u.thinkingActive {
		// No "thinking" header on purpose: the streamed bullets prefixed
		// with `│` already convey the panel, and EndThinking will close
		// it with a single `┄ thought for Xs` footer. Two lines bracketing
		// every reasoning panel was visual noise.
		fmt.Fprintf(u.out, "%s ", u.style.Dim(iconBar))
		u.thinkingActive = true
		u.thinkingStart = time.Now()
	}
	indented := strings.ReplaceAll(text, "\n", "\n"+iconBar+" ")
	fmt.Fprint(u.out, u.style.Dim(indented))
}

// EndThinking closes the dim thinking panel if one is open, printing a
// "thought for Xs" footer so the user can see how long the model spent
// reasoning before producing the visible answer.
func (u *PlainUI) EndThinking() {
	u.stopSpinner()
	u.mu.Lock()
	defer u.mu.Unlock()
	u.closeThinkingLocked(true)
}

// splitThinking pulls one <thinking>...</thinking> (or <thinking>...) segment from
// the buffer if present. It returns the text outside the tag (visible), the
// inner thinking text, the unconsumed leftover, and whether it consumed a
// full tag (so the caller can keep splitting).
func splitThinking(buf string) (visible, thinking, leftover string, hasMore bool) {
	openIdx, openTag := findOpenThink(buf)
	if openIdx < 0 {
		if cut := safeFlushPoint(buf); cut > 0 {
			return buf[:cut], "", buf[cut:], false
		}
		return "", "", buf, false
	}
	visible = buf[:openIdx]
	rest := buf[openIdx+len(openTag):]

	closeTag := strings.Replace(openTag, "<", "</", 1)
	endIdx := strings.Index(rest, closeTag)
	if endIdx < 0 {
		return visible, rest, "", false
	}
	thinking = rest[:endIdx]
	leftover = rest[endIdx+len(closeTag):]
	hasMore = true
	return
}

// findOpenThink finds the first opening think-style tag.
func findOpenThink(buf string) (int, string) {
	candidates := []string{"<think>", "<thinking>"}
	bestIdx := -1
	bestTag := ""
	for _, tag := range candidates {
		if i := strings.Index(buf, tag); i >= 0 && (bestIdx < 0 || i < bestIdx) {
			bestIdx = i
			bestTag = tag
		}
	}
	return bestIdx, bestTag
}

// safeFlushPoint returns the largest index up to which buf is safe to print
// without losing the start of an in-progress tag like "<thi".
func safeFlushPoint(buf string) int {
	if len(buf) == 0 {
		return 0
	}
	if i := strings.LastIndexByte(buf, '<'); i >= 0 {
		tail := buf[i:]
		if len(tail) <= len("<thinking>") {
			for _, tag := range []string{"<think>", "<thinking>"} {
				if strings.HasPrefix(tag, tail) {
					return i
				}
			}
		}
	}
	return len(buf)
}
