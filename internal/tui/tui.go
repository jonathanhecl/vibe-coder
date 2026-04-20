package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	"golang.org/x/term"
)

// PlainUI renders an interactive session in a way inspired by Cursor's chat:
// streamed assistant text, a single-line tool "card" that gets rewritten in
// place when the result arrives, and dim "thinking" sections.
type PlainUI struct {
	in  *os.File
	out io.Writer

	reader *bufio.Reader
	style  Style

	mu       sync.Mutex
	stopCh   chan struct{}
	stopOnce sync.Once
	rawFD    int
	rawState *term.State

	pendingTool   string
	pendingHeader string
	pendingActive bool

	streamingAssistant bool
	thinkingActive     bool
	streamBuffer       strings.Builder
}

type Decision int

const (
	DecisionDeny Decision = iota
	DecisionAllowOnce
	DecisionAllowAll
	DecisionDenyAll
	DecisionYesMode
)

const (
	iconUser      = "›"
	iconAssistant = "●"
	iconRunning   = "▸"
	iconOk        = "✓"
	iconErr       = "✗"
	iconThink     = "…"
)

// NewPlain constructs a PlainUI bound to standard streams. Colors are emitted
// only when stdout is a TTY and NO_COLOR is unset.
func NewPlain() *PlainUI {
	return &PlainUI{
		in:     os.Stdin,
		out:    os.Stdout,
		reader: bufio.NewReader(os.Stdin),
		style:  NewStyle(os.Stdout),
		stopCh: make(chan struct{}),
		rawFD:  int(os.Stdin.Fd()),
	}
}

// StreamAssistant prints assistant tokens as they arrive. It strips
// <think>...</think> blocks from the visible reply and re-routes them to a
// dimmed thinking section so they read like Cursor's reasoning panel.
func (u *PlainUI) StreamAssistant(text string) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.flushPendingToolLocked()

	if !u.streamingAssistant {
		if u.style.Enabled() {
			fmt.Fprintf(u.out, "%s %s ", u.style.Cyan(iconAssistant), u.style.Bold("assistant"))
		}
		u.streamingAssistant = true
	}

	u.streamBuffer.WriteString(text)
	for {
		buf := u.streamBuffer.String()
		visible, thinking, leftover, hasMore := splitThinking(buf)

		if visible != "" {
			fmt.Fprint(u.out, visible)
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

// EndAssistant marks the end of an assistant turn and prints a trailing
// newline so the next prompt lines up cleanly.
func (u *PlainUI) EndAssistant() {
	u.mu.Lock()
	defer u.mu.Unlock()

	if rest := u.streamBuffer.String(); rest != "" {
		fmt.Fprint(u.out, rest)
		u.streamBuffer.Reset()
	}
	if u.thinkingActive {
		fmt.Fprint(u.out, u.style.Dim("\n"))
		u.thinkingActive = false
	}
	if u.streamingAssistant {
		fmt.Fprintln(u.out)
		u.streamingAssistant = false
	}
}

// ShowToolCall prints a single-line tool card that will be rewritten in place
// when ShowToolResult arrives, mimicking Cursor's collapsing tool cards.
func (u *PlainUI) ShowToolCall(name string, params map[string]any) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.streamingAssistant {
		fmt.Fprintln(u.out)
		u.streamingAssistant = false
	}
	u.flushPendingToolLocked()

	header := fmt.Sprintf("%s %s%s",
		u.style.Yellow(iconRunning),
		u.style.Bold(name),
		u.style.Dim(formatParams(params)),
	)
	fmt.Fprintf(u.out, "%s %s", header, u.style.Dim("running…"))

	u.pendingTool = name
	u.pendingHeader = fmt.Sprintf("%s%s",
		u.style.Bold(name),
		u.style.Dim(formatParams(params)),
	)
	u.pendingActive = true
}

// ShowToolResult collapses the in-progress tool card into a final state,
// printing a one-line summary of the output rather than dumping everything.
// Long outputs are truncated; the user sees the same "✓ Read foo.go (1.2KB)"
// style that Cursor uses.
func (u *PlainUI) ShowToolResult(name, output string, isError bool) {
	u.mu.Lock()
	defer u.mu.Unlock()

	icon := u.style.Green(iconOk)
	tag := u.style.Dim("done")
	if isError {
		icon = u.style.Red(iconErr)
		tag = u.style.Red("error")
	}

	header := u.pendingHeader
	if !u.pendingActive || u.pendingTool != name {
		header = fmt.Sprintf("%s", u.style.Bold(name))
	}

	summary := summarizeOutput(output)

	if u.pendingActive {
		fmt.Fprint(u.out, u.style.ClearPendingLine())
	}
	fmt.Fprintf(u.out, "%s %s %s %s\n", icon, header, tag, u.style.Dim("→ "+summary))

	if isError && strings.TrimSpace(output) != "" {
		printIndented(u.out, u.style.Dim(strings.TrimRight(output, "\n")))
	}

	u.pendingActive = false
	u.pendingTool = ""
	u.pendingHeader = ""
}

// GetInput reads a line from stdin, supporting a ";;...;;" multi-line marker.
func (u *PlainUI) GetInput(prompt string) (string, error) {
	u.mu.Lock()
	u.flushPendingToolLocked()
	if u.streamingAssistant {
		fmt.Fprintln(u.out)
		u.streamingAssistant = false
	}
	if u.style.Enabled() {
		_, _ = io.WriteString(u.out, fmt.Sprintf("%s %s", u.style.Cyan(iconUser), prompt))
	} else {
		_, _ = io.WriteString(u.out, prompt)
	}
	u.mu.Unlock()

	line, err := u.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = trimLine(line)
	if strings.TrimSpace(line) != ";;" {
		return line, nil
	}

	lines := make([]string, 0, 8)
	for {
		_, _ = io.WriteString(u.out, u.style.Dim("... "))
		next, err := u.reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		next = trimLine(next)
		if strings.TrimSpace(next) == ";;" {
			break
		}
		lines = append(lines, next)
	}
	return strings.Join(lines, "\n"), nil
}

// AskPermission prompts the user with a colored question for tool consent.
func (u *PlainUI) AskPermission(tool string, params map[string]any) Decision {
	u.mu.Lock()
	u.flushPendingToolLocked()
	question := fmt.Sprintf("%s %s%s",
		u.style.Yellow("?"),
		u.style.Bold("Allow "+tool),
		u.style.Dim(formatParams(params)),
	)
	choices := u.style.Dim(" [y]es / [n]o / [a]ll / [d]eny-all / [s]kip-all-confirm: ")
	_, _ = fmt.Fprint(u.out, question, choices)
	u.mu.Unlock()

	line, err := u.reader.ReadString('\n')
	if err != nil {
		return DecisionDeny
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return DecisionAllowOnce
	case "a", "all":
		return DecisionAllowAll
	case "d", "deny-all":
		return DecisionDenyAll
	case "s", "skip-all-confirm":
		return DecisionYesMode
	default:
		return DecisionDeny
	}
}

// StartESCMonitor switches the terminal to raw mode so we can detect ESC
// without waiting for a newline. Always paired with StopESCMonitor.
func (u *PlainUI) StartESCMonitor(interrupt func()) error {
	if !term.IsTerminal(u.rawFD) {
		return nil
	}

	u.mu.Lock()
	if u.rawState != nil {
		u.mu.Unlock()
		return nil
	}
	state, err := term.MakeRaw(u.rawFD)
	if err != nil {
		u.mu.Unlock()
		return fmt.Errorf("enable raw mode: %w", err)
	}
	u.rawState = state
	u.mu.Unlock()

	go func() {
		buf := make([]byte, 1)
		for {
			select {
			case <-u.stopCh:
				return
			default:
			}
			n, err := u.in.Read(buf)
			if err != nil || n == 0 {
				return
			}
			if buf[0] == 0x1B {
				interrupt()
				return
			}
		}
	}()
	return nil
}

// StopESCMonitor restores the terminal cooked mode. Safe to call multiple
// times; this is what protects the user from a corrupted terminal after
// abrupt exits.
func (u *PlainUI) StopESCMonitor() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.rawState != nil {
		_ = term.Restore(u.rawFD, u.rawState)
		u.rawState = nil
	}
}

// Stop performs a one-time shutdown of the UI: closes the ESC monitor and
// restores the terminal. Idempotent so signal handlers can call it safely.
func (u *PlainUI) Stop() {
	u.stopOnce.Do(func() {
		close(u.stopCh)
	})
	u.StopESCMonitor()
}

// flushPendingToolLocked closes any tool card that was left in the
// "running…" state without a matching ShowToolResult.
func (u *PlainUI) flushPendingToolLocked() {
	if !u.pendingActive {
		return
	}
	fmt.Fprint(u.out, u.style.ClearPendingLine())
	fmt.Fprintf(u.out, "%s %s %s\n",
		u.style.Yellow("•"), u.pendingHeader, u.style.Dim("interrupted"),
	)
	u.pendingActive = false
	u.pendingTool = ""
	u.pendingHeader = ""
}

func (u *PlainUI) writeThinkingChunkLocked(text string) {
	if !u.thinkingActive {
		fmt.Fprintf(u.out, "\n%s %s ", u.style.Gray(iconThink), u.style.Gray("thinking"))
		u.thinkingActive = true
	}
	fmt.Fprint(u.out, u.style.Gray(text))
}

// splitThinking pulls one <think>...</think> (or <thinking>...) segment from
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

// formatParams renders a compact (key=val, ...) suffix with a stable order so
// tool cards don't jitter between runs.
func formatParams(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		val := truncateInline(fmt.Sprintf("%v", params[k]), 60)
		parts = append(parts, fmt.Sprintf("%s=%s", k, val))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// summarizeOutput collapses tool output to a single short line for the card.
func summarizeOutput(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "no output"
	}
	lines := strings.Split(trimmed, "\n")
	bytes := FormatBytes(len(output))
	if len(lines) > 1 {
		return fmt.Sprintf("%d lines, %s", len(lines), bytes)
	}
	if len(trimmed) > 80 {
		return truncateInline(trimmed, 80)
	}
	return trimmed
}

func truncateInline(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return s[:max-1] + "…"
}

func printIndented(w io.Writer, s string) {
	for _, line := range strings.Split(s, "\n") {
		fmt.Fprintln(w, "    "+line)
	}
}

func trimLine(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
