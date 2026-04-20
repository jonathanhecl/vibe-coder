package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// PlainUI renders an interactive session in a way inspired by Cursor's chat:
// streamed assistant text, a single-line tool "card" that gets rewritten in
// place when the result arrives, and dim "thinking" sections.
//
// Important: PlainUI never puts the terminal in raw mode. We rely on the OS
// signal handler in main to translate Ctrl+C into a clean shutdown. Raw mode
// would silently swallow Ctrl+C (it becomes byte 0x03) and would also race
// with bufio readers on stdin, leaving the user unable to type or exit.
type PlainUI struct {
	in  *os.File
	out io.Writer

	reader *bufio.Reader
	style  Style

	mu       sync.Mutex
	stopCh   chan struct{}
	stopOnce sync.Once

	pendingTool   string
	pendingHeader string
	pendingActive bool

	streamingAssistant bool
	thinkingActive     bool
	thinkingStart      time.Time
	streamBuffer       strings.Builder

	// spinner state. spinnerMu guards spinner only (a small lock that does
	// NOT cover stdout writes); the running goroutine takes mu to paint
	// frames, so callers must never hold mu when manipulating spinner.
	spinnerMu sync.Mutex
	spinner   *spinner
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
	iconRule      = "┄"
	iconBar       = "│"
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
	}
}

// StreamAssistant prints assistant tokens as they arrive. It strips
// <think>...</think> blocks from the visible reply and re-routes them to a
// dimmed thinking section so they read like Cursor's reasoning panel.
func (u *PlainUI) StreamAssistant(text string) {
	u.stopSpinner()
	u.mu.Lock()
	defer u.mu.Unlock()

	u.flushPendingToolLocked()

	if !u.streamingAssistant {
		if u.style.Enabled() {
			fmt.Fprintf(u.out, "%s %s ",
				u.style.BrightGreen(iconAssistant),
				u.style.BoldGreen("assistant"),
			)
		}
		u.streamingAssistant = true
	}

	u.streamBuffer.WriteString(text)
	for {
		buf := u.streamBuffer.String()
		visible, thinking, leftover, hasMore := splitThinking(buf)

		if visible != "" {
			fmt.Fprint(u.out, u.style.BrightWhite(visible))
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
	u.stopSpinner()
	u.mu.Lock()
	defer u.mu.Unlock()

	if rest := u.streamBuffer.String(); rest != "" {
		fmt.Fprint(u.out, u.style.BrightWhite(rest))
		u.streamBuffer.Reset()
	}
	if u.thinkingActive {
		elapsed := formatElapsed(time.Since(u.thinkingStart))
		fmt.Fprintf(u.out, "\n%s %s\n",
			u.style.Dim(iconRule),
			u.style.Dim("thought for "+elapsed),
		)
		u.thinkingActive = false
	}
	if u.streamingAssistant {
		fmt.Fprintln(u.out)
		u.streamingAssistant = false
	}
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
	if u.streamingAssistant {
		fmt.Fprintln(u.out)
		u.streamingAssistant = false
	}
	if !u.thinkingActive {
		fmt.Fprintf(u.out, "\n%s %s\n%s ",
			u.style.Dim(iconRule),
			u.style.Dim("thinking"),
			u.style.Dim(iconBar),
		)
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
	if u.thinkingActive {
		elapsed := formatElapsed(time.Since(u.thinkingStart))
		fmt.Fprintf(u.out, "\n%s %s\n",
			u.style.Dim(iconRule),
			u.style.Dim("thought for "+elapsed),
		)
		u.thinkingActive = false
	}
}

// ShowToolCall opens a "running" tool card. In TTY mode the card is rendered
// by the spinner goroutine so the user sees an animated indicator that
// proves the agent is alive even on slow tools (Bash, network calls).
// ShowToolResult later replaces that line with the final ✓/✗ summary.
//
// In non-TTY mode (NO_COLOR / redirected stdout / tests) we just record the
// pending state without animation; the result line is printed verbatim.
func (u *PlainUI) ShowToolCall(name string, params map[string]any) {
	u.stopSpinner()
	u.mu.Lock()

	if u.streamingAssistant {
		fmt.Fprintln(u.out)
		u.streamingAssistant = false
	}
	if u.thinkingActive {
		fmt.Fprintln(u.out)
		u.thinkingActive = false
	}
	u.flushPendingToolLocked()

	u.pendingTool = name
	u.pendingHeader = fmt.Sprintf("%s%s",
		u.style.BoldBlue(name),
		u.style.DimBlue(formatParams(params)),
	)
	u.pendingActive = true

	enabled := u.style.Enabled()
	u.mu.Unlock()

	if !enabled {
		return
	}
	label := fmt.Sprintf("%s%s %s",
		u.style.BoldBlue(name),
		u.style.DimBlue(formatParams(params)),
		u.style.DimBlue("running…"),
	)
	u.startSpinner(label)
}

// ShowToolResult collapses the in-progress tool card into a final state,
// printing a one-line summary of the output rather than dumping everything.
// Long outputs are truncated; the user sees the same "✓ Read foo.go (1.2KB)"
// style that Cursor uses.
func (u *PlainUI) ShowToolResult(name, output string, isError bool) {
	u.stopSpinner()
	u.mu.Lock()
	defer u.mu.Unlock()

	icon := u.style.BrightGreen(iconOk)
	tag := u.style.DimGreen("done")
	summaryColor := u.style.DimGreen
	if isError {
		icon = u.style.BoldRed(iconErr)
		tag = u.style.Red("error")
		summaryColor = u.style.Red
	}

	header := u.pendingHeader
	if !u.pendingActive || u.pendingTool != name {
		header = u.style.BoldBlue(name)
	}

	summary := summarizeOutput(output)

	if u.pendingActive {
		fmt.Fprint(u.out, u.style.ClearPendingLine())
	}
	fmt.Fprintf(u.out, "%s %s %s %s\n", icon, header, tag, summaryColor("→ "+summary))

	if isError && strings.TrimSpace(output) != "" {
		printIndented(u.out, u.style.Red(strings.TrimRight(output, "\n")))
	}

	u.pendingActive = false
	u.pendingTool = ""
	u.pendingHeader = ""
}

// GetInput reads a line from stdin, supporting a ";;...;;" multi-line marker.
func (u *PlainUI) GetInput(prompt string) (string, error) {
	u.stopSpinner()
	u.mu.Lock()
	u.flushPendingToolLocked()
	if u.streamingAssistant {
		fmt.Fprintln(u.out)
		u.streamingAssistant = false
	}
	if u.style.Enabled() {
		_, _ = io.WriteString(u.out, fmt.Sprintf("%s %s %s",
			u.style.BrightGreen(iconUser),
			u.style.BoldGreen("user"),
			u.style.BoldGreen(prompt),
		))
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
		_, _ = io.WriteString(u.out, u.style.DimGreen("... "))
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
	u.stopSpinner()
	u.mu.Lock()
	u.flushPendingToolLocked()
	question := fmt.Sprintf("%s %s%s",
		u.style.BoldYellow("?"),
		u.style.BoldYellow("Allow "+tool),
		u.style.Yellow(formatParams(params)),
	)
	choices := u.style.DimGreen(" [y]es / [n]o / [a]ll / [d]eny-all / [s]kip-all-confirm: ")
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

// StartESCMonitor is a no-op kept for backwards compatibility with the
// agent's uiPort interface. We intentionally do not enter raw mode anymore;
// see the type comment for why. Cancellation is now driven by the OS signal
// handler in main, which cancels the root context on Ctrl+C.
func (u *PlainUI) StartESCMonitor(interrupt func()) error { return nil }

// StopESCMonitor is a no-op kept for backwards compatibility.
func (u *PlainUI) StopESCMonitor() {}

// Stop performs a one-time shutdown of the UI. Idempotent so signal handlers
// can call it safely. It also halts the spinner so a Ctrl+C doesn't leave a
// half-painted Braille frame on the user's terminal.
func (u *PlainUI) Stop() {
	u.stopSpinner()
	u.stopOnce.Do(func() {
		close(u.stopCh)
	})
}

// flushPendingToolLocked closes any tool card that was left in the
// "running…" state without a matching ShowToolResult.
func (u *PlainUI) flushPendingToolLocked() {
	if !u.pendingActive {
		return
	}
	fmt.Fprint(u.out, u.style.ClearPendingLine())
	fmt.Fprintf(u.out, "%s %s %s\n",
		u.style.BoldYellow("•"), u.pendingHeader, u.style.Yellow("interrupted"),
	)
	u.pendingActive = false
	u.pendingTool = ""
	u.pendingHeader = ""
}

func (u *PlainUI) writeThinkingChunkLocked(text string) {
	if !u.thinkingActive {
		fmt.Fprintf(u.out, "\n%s %s ", u.style.DimGreen(iconThink), u.style.DimGreen("thinking"))
		u.thinkingActive = true
	}
	fmt.Fprint(u.out, u.style.DimGreen(text))
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
