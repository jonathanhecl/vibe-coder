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
// distinct so a timeout is announced and a deliberate ESC is not. errAskTick is
// not an error the user sees: it asks the line editor to repaint the countdown
// and keep reading.
var (
	errAskEscape  = errors.New("ask: escaped")
	errAskTimeout = errors.New("ask: timed out")
	errAskTick    = errors.New("ask: repaint countdown")
)

// askEscapeWindow is how long a lone ESC waits for the rest of a sequence
// before it counts as "give me the default answer". Arrow keys and bracketed
// paste arrive as a burst, so they never trip this.
const askEscapeWindow = 40 * time.Millisecond

// AskOptions draws card and reads the answer through the UI's own line editor,
// so typed characters are echoed and edited like any other prompt.
//
// ESC (or reaching the user-action timeout) returns the empty answer the tool
// treats as option 1, which is why option 1 is documented as the recommended
// one.
//
// The budget is the permission timeout: both prompts are a terminal waiting on
// a human, so one setting covers both.
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
	deadline := u.askDeadline()
	if !deadline.IsZero() && !u.countdownAvailable() {
		// The countdown is repainted with escape sequences on the prompt line, so
		// it needs a terminal on both ends. Where it cannot be shown, waiting for
		// the user beats taking a default nobody saw coming.
		deadline = time.Time{}
	}
	promptFn := func() string { return u.askPromptLine(len(card.Options), time.Until(deadline)) }
	renderedPrompt := promptFn()
	_, _ = io.WriteString(u.out, "\n"+renderedPrompt)
	u.mu.Unlock()

	if u.in == nil || !term.IsTerminal(int(u.in.Fd())) {
		line, err := u.reader.ReadString('\n')
		return strings.TrimSpace(line), err
	}

	input := &askInput{br: u.reader, fd: int(u.in.Fd()), deadline: deadline}
	line, err := u.readInteractiveInputFromFn(input, renderedPrompt, promptFn)
	switch {
	case errors.Is(err, errAskTimeout):
		u.reportAskTimeout(card)
		return "", nil
	case errors.Is(err, errAskEscape):
		return "", nil
	}
	return line, err
}

// askDeadline turns the user-action timeout into an absolute deadline. It is
// the same budget a permission prompt gets: both are the terminal waiting on a
// human, so there is one setting instead of two. A zero time means "wait for
// the user", which is what an explicit off asks for.
func (u *PlainUI) askDeadline() time.Time {
	if u.cfg == nil {
		return time.Time{}
	}
	d := u.cfg.EffectivePermissionTimeout()
	if d <= 0 {
		return time.Time{}
	}
	return time.Now().Add(d)
}

func (u *PlainUI) reportAskTimeout(card AskCard) {
	label := ""
	if len(card.Options) > 0 {
		label = card.Options[0].Label
	}
	waited := time.Duration(0)
	if u.cfg != nil {
		waited = u.cfg.EffectivePermissionTimeout()
	}
	// The timeout can interrupt a half-typed answer, so start on a fresh line.
	// The wording matches the permission card: both prompts spend the same budget
	// and announce the same way what they did with it.
	fmt.Fprintf(u.out, "\r\n  %s\n",
		u.style.DimYellow(fmt.Sprintf("⏳ no answer after %s — using the default [1] %s.", formatBadgeWait(waited), label)),
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

// askPromptLine renders the answer line. A positive remaining duration adds a
// live countdown, so the timeout is something the user watches instead of
// something that surprises them.
func (u *PlainUI) askPromptLine(optionCount int, remaining time.Duration) string {
	return optionPromptLine(u.style, askBadge(remaining), fmt.Sprintf("Select option (1-%d) [default: 1]", optionCount))
}

// optionPromptLine is the input line every interactive card ends with: a caret,
// an optional countdown badge, the instruction and the colon the cursor sits
// after. The question card and the permission card share it so the two prompts
// read as one interface instead of two, and so a change to the shape happens in
// one place.
//
// The badge comes before the instruction: a fixed column means the line never
// shifts while the clock runs, and the clock reads as a badge rather than as the
// last word of a sentence.
func optionPromptLine(st Style, badge, instruction string) string {
	if !st.Enabled() {
		if badge == "" {
			return "  ▸ " + instruction + ": "
		}
		return "  ▸ " + badge + "  " + instruction + ": "
	}
	line := "  " + st.BoldCyan("▸") + " "
	if badge != "" {
		line += st.DimYellow(badge) + "  "
	}
	return line + st.Dim(instruction) + ": "
}

// askBadge is the visible side of the user-action timeout: the prompt counts
// down, so the deadline is something the user watched rather than a surprise.
// An empty string means there is no clock to show.
//
// m:ss, not raw seconds: at minute scale "180s" reads like a bug while "3:00"
// reads like a clock. Both cards spend the same budget, so both use this badge
// and the user only has to learn one clock.
func askBadge(remaining time.Duration) string {
	if remaining <= 0 {
		return ""
	}
	secs := ceilSeconds(remaining)
	if secs < 1 {
		secs = 1
	}
	return "⏳ " + formatBadgeWait(time.Duration(secs)*time.Second)
}

// formatBadgeWait renders a wait the way the badge does (m:ss) without the
// glyph, for sentences that talk about a wait that already happened. Seconds are
// rounded up so a fresh 3-minute prompt reads 3:00 and never 2:59.
func formatBadgeWait(d time.Duration) string {
	secs := ceilSeconds(d)
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
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
	ch, err := a.nextByte()
	if err != nil {
		return 0, err
	}
	buf := p[:1]
	buf[0] = ch
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

// nextByte waits for one keystroke. A countdown tick is reported as errAskTick
// so the line editor repaints the prompt and asks again; real input always wins
// because waitByte only ticks after a poll came back empty.
func (a *askInput) nextByte() (byte, error) {
	if a.deadline.IsZero() {
		return a.br.ReadByte()
	}
	ch, err := waitByte(a.fd, a.br, a.deadline, func(time.Duration) error { return errAskTick })
	if errors.Is(err, errPromptTimeout) {
		return 0, errAskTimeout
	}
	return ch, err
}
