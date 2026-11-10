package tui

import (
	"bytes"
	"strings"
	"testing"
)

// A reply that only thinks and then calls a tool must not move the cursor up
// over the previous reply: its line count belongs to that earlier reply.
func TestCollapseAfterThinkingOnlyReplyDoesNotEraseEarlierReply(t *testing.T) {
	var buf bytes.Buffer
	u := &PlainUI{out: &buf, style: NewStyleForTest(true)}

	u.StreamAssistant("first line\nsecond line\nthird line\n")
	u.EndAssistant(TurnUsage{})

	buf.Reset()
	u.StreamThinking("let me check the file")
	u.EndThinking()
	u.EndAssistant(TurnUsage{})
	u.CollapseAssistantOutput()

	if strings.Contains(buf.String(), "A\x1b[J") {
		t.Fatalf("collapse moved the cursor over the previous reply: %q", buf.String())
	}
}

// A reply with visible text that is collapsed still erases its own lines.
func TestCollapseErasesCurrentReplyLines(t *testing.T) {
	var buf bytes.Buffer
	u := &PlainUI{out: &buf, style: NewStyleForTest(true)}

	u.StreamAssistant("one\ntwo\n")
	u.EndAssistant(TurnUsage{})
	u.CollapseAssistantOutput()

	if !strings.Contains(buf.String(), "A\x1b[J") {
		t.Fatalf("collapse did not erase the current reply: %q", buf.String())
	}
}
