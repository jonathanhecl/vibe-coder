package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

// AskOption is one choice in an AskCard. Description is optional and rendered
// dimmed beside the label, so a question can explain the tradeoff of each
// answer without lengthening the label itself.
type AskOption struct {
	Label       string
	Description string
}

// AskCard is a multiple-choice question the agent asks through AskOptions.
// Options are rendered as numbered rows; the answer line is read back as typed.
type AskCard struct {
	Title   string
	Prompt  string
	Options []AskOption
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

// errAskEscape and errAskTimeout both mean "take the default option". They stay
// distinct so a timeout is announced and a deliberate ESC is not.
var (
	errAskEscape  = errors.New("ask: escaped")
	errAskTimeout = errors.New("ask: timed out")
)

// askEscapeWindow is how long a lone ESC waits for the rest of a sequence
// before it counts as "give me the default answer". Arrow keys and bracketed
// paste arrive as a burst, so they never trip this.
const askEscapeWindow = 40 * time.Millisecond

// AskOptions draws card and reads the answer through the UI's own line editor,
// so typed characters are echoed and edited like any other prompt.
//
// ESC (or reaching the configured ASK_TIMEOUT_MS) returns the empty answer the
// tool treats as option 1, which is why option 1 is documented as the
// recommended one.
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

	input := &askInput{br: u.reader, fd: int(u.in.Fd()), deadline: u.askDeadline()}
	line, err := u.readInteractiveInputFrom(input, renderedPrompt)
	switch {
	case errors.Is(err, errAskTimeout):
		u.reportAskTimeout(card)
		return "", nil
	case errors.Is(err, errAskEscape):
		return "", nil
	}
	return line, err
}

// askDeadline turns the configured ask timeout into an absolute deadline. A
// zero time means "wait for the user", which is the default.
func (u *PlainUI) askDeadline() time.Time {
	if u.cfg == nil || u.cfg.Harness.AskTimeoutMS <= 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(u.cfg.Harness.AskTimeoutMS) * time.Millisecond)
}

func (u *PlainUI) reportAskTimeout(card AskCard) {
	label := ""
	if len(card.Options) > 0 {
		label = card.Options[0].Label
	}
	waited := time.Duration(u.cfg.Harness.AskTimeoutMS) * time.Millisecond
	// The timeout can interrupt a half-typed answer, so start on a fresh line.
	fmt.Fprintf(u.out, "\r\n  %s\n",
		u.style.DimYellow(fmt.Sprintf("No answer after %s: using the default [1] %s.", waited.Round(time.Second), label)),
	)
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
			b.WriteString("    ")
			b.WriteString(u.style.BoldCyan("[" + strconv.Itoa(i+1) + "]"))
			b.WriteString(" ")
			b.WriteString(u.style.BrightWhite(opt.Label))
			if opt.Description != "" {
				b.WriteString("  ")
				b.WriteString(u.style.Dim(opt.Description))
			}
			b.WriteString("\n")
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

// askInput reads terminal bytes through the shared buffered reader while
// honouring two ways out of a question: a lone ESC, and the configured
// deadline. Both report "use the default" instead of blocking forever.
type askInput struct {
	br       *bufio.Reader
	fd       int
	deadline time.Time
}

func (a *askInput) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if a.br.Buffered() == 0 {
		timeout := time.Duration(-1)
		if !a.deadline.IsZero() {
			remaining := time.Until(a.deadline)
			if remaining <= 0 {
				return 0, errAskTimeout
			}
			timeout = remaining
		}
		ready, err := waitReadable(a.fd, timeout)
		if err != nil {
			return 0, err
		}
		if !ready {
			return 0, errAskTimeout
		}
	}

	buf := p[:1]
	if _, err := a.br.Read(buf); err != nil {
		return 0, err
	}
	if buf[0] != 0x1b || a.br.Buffered() > 0 {
		return 1, nil
	}
	// A bare ESC with nothing behind it is the user asking for the default.
	// Anything else (arrows, Alt-Enter, paste markers) already has the rest of
	// the sequence buffered by now.
	ready, err := waitReadable(a.fd, askEscapeWindow)
	if err != nil {
		return 1, nil
	}
	if !ready {
		return 0, errAskEscape
	}
	return 1, nil
}
