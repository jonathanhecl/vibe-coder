package tui

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// AskCard is a multiple-choice question the agent asks through AskOptions.
// Options are rendered as numbered rows; the answer line is read back as typed.
type AskCard struct {
	Title   string
	Prompt  string
	Options []string
	// Note explains why the previous answer was rejected. When set, only the
	// note and the prompt are redrawn, which keeps a retry from repeating the
	// whole card.
	Note string
}

// OptionAsker is implemented by UIs that can present an AskCard. It stays out
// of UI so a front end that cannot ask is not forced to fake a prompt.
type OptionAsker interface {
	AskOptions(card AskCard) (string, error)
}

// AskOptions draws card and reads the answer through the UI's own line editor,
// so typed characters are echoed and edited like any other prompt.
//
// The ESC monitor is paused first: while it runs it holds stdin in
// non-canonical mode with echo disabled and consumes keystrokes, which is
// exactly what made answers invisible when a tool read stdin by itself.
func (u *PlainUI) AskOptions(card AskCard) (string, error) {
	if interrupt, paused := u.pauseESCMonitor(); paused {
		defer func() { _ = u.StartESCMonitor(interrupt) }()
	}

	u.stopSpinner()
	u.mu.Lock()
	u.flushPendingToolLocked()
	u.endAssistantLineLocked()
	u.renderAskCardLocked(card)
	// The line editor only repaints the editable region; the prompt itself has
	// to be on screen before it starts, exactly like GetInput does. The blank
	// line before it belongs to the card, not to the prompt handed to the
	// editor, which measures the prompt to place the cursor.
	renderedPrompt := u.askPromptLine(len(card.Options))
	_, _ = io.WriteString(u.out, "\n"+renderedPrompt)
	u.mu.Unlock()

	if u.in == nil || !term.IsTerminal(int(u.in.Fd())) {
		line, err := u.reader.ReadString('\n')
		return strings.TrimSpace(line), err
	}
	// readInteractiveInput switches stdin to raw mode and repaints the prompt
	// line as the user types, which is the echo the tool path lacked.
	return u.readInteractiveInput(renderedPrompt)
}

// pauseESCMonitor stops the double-ESC watcher and returns the interrupt it was
// armed with so the caller can restore it. The second result is false when no
// monitor was running.
func (u *PlainUI) pauseESCMonitor() (func(), bool) {
	u.escMu.Lock()
	interrupt := u.escCancel
	running := u.escStop != nil
	u.escMu.Unlock()
	if !running {
		return nil, false
	}
	u.StopESCMonitor()
	if interrupt == nil {
		return nil, false
	}
	return interrupt, true
}

func (u *PlainUI) renderAskCardLocked(card AskCard) {
	var b strings.Builder
	title := strings.TrimSpace(card.Title)
	prompt := strings.TrimSpace(card.Prompt)

	if card.Note != "" {
		b.WriteString(fmt.Sprintf("  %s\n", u.style.DimYellow(card.Note)))
	} else if title != "" {
		b.WriteString(fmt.Sprintf("\n  %s %s\n    %s\n\n",
			u.style.BoldCyan("?"),
			u.style.Bold(title),
			u.style.BrightWhite(prompt),
		))
	} else {
		b.WriteString(fmt.Sprintf("\n  %s %s\n\n",
			u.style.BoldCyan("?"),
			u.style.Bold(prompt),
		))
	}

	if card.Note == "" {
		for i, opt := range card.Options {
			b.WriteString(fmt.Sprintf("    %s %s\n",
				u.style.BoldCyan("["+strconv.Itoa(i+1)+"]"),
				u.style.BrightWhite(opt),
			))
		}
	}
	_, _ = io.WriteString(u.out, b.String())
}

func (u *PlainUI) askPromptLine(optionCount int) string {
	label := fmt.Sprintf("Select option (1-%d) [default: 1]: ", optionCount)
	if u.style.Enabled() {
		return "  " + u.style.BoldCyan("▸") + " " + u.style.Dim(label)
	}
	return "  ▸ " + label
}
