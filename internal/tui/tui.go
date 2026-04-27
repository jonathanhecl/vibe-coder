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

// UI is the shared contract between main/agent and concrete terminal
// renderers (plain and rich).
type UI interface {
	StartESCMonitor(interrupt func()) error
	StopESCMonitor()
	SetPlanMode(enabled bool)
	StreamAssistant(text string)
	EndAssistant()
	StreamThinking(text string)
	EndThinking()
	StartWaiting(label string)
	StopWaiting()
	ShowToolCall(name string, params map[string]any)
	ShowToolResult(name, output string, isError bool, toolParams map[string]any)
	ShowTodos(items []TodoItem)
	AskPermission(tool string, params map[string]any) Decision
	GetInput(prompt string) (string, error)
	Stop()
}

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

	reader   *bufio.Reader
	style    Style
	planMode bool

	mu       sync.Mutex
	stopCh   chan struct{}
	stopOnce sync.Once

	pendingTool   string
	pendingHeader string
	pendingActive bool

	streamingAssistant  bool
	thinkingActive      bool
	thinkingStart       time.Time
	assistantReplyStart time.Time
	assistantHadVisible bool
	streamBuffer        strings.Builder
	markdown            *MarkdownRenderer

	// spinner state. spinnerMu guards spinner only (a small lock that does
	// NOT cover stdout writes); the running goroutine takes mu to paint
	// frames, so callers must never hold mu when manipulating spinner.
	spinnerMu sync.Mutex
	spinner   *spinner
}

type Decision int

const (
	DecisionDenyOnce Decision = iota
	DecisionAllowOnce
	DecisionAllowSession
	DecisionAllowPersistent
	DecisionDenySession
	DecisionDenyPersistent
	DecisionYesMode
	DecisionCancel
)

// Backward-compatible aliases for older call sites and tests.
const (
	DecisionDeny     = DecisionDenyOnce
	DecisionAllowAll = DecisionAllowPersistent
	DecisionDenyAll  = DecisionDenyPersistent
)

const (
	iconUser      = "👤"
	iconAssistant = "🤖"
	iconRunning   = "🔄"
	iconTool      = "🔨"
	iconDone      = "✅"
	iconErr       = "❌"
	iconThink     = "☁️"
	iconRule      = "🕑"
	iconBar       = "💭"
)

// NewPlain constructs a PlainUI bound to standard streams. Colors are emitted
// only when stdout is a TTY and NO_COLOR is unset.
func NewPlain() *PlainUI {
	st := NewStyle(os.Stdout)
	return &PlainUI{
		in:       os.Stdin,
		out:      os.Stdout,
		reader:   bufio.NewReader(os.Stdin),
		style:    st,
		stopCh:   make(chan struct{}),
		markdown: NewMarkdownRenderer(st),
	}
}

// SetPlanMode switches UI accent colors for role labels between plan/build.
func (u *PlainUI) SetPlanMode(enabled bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.planMode = enabled
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
		u.ensureMarkdownLocked()
	}

	u.streamBuffer.WriteString(text)
	for {
		buf := u.streamBuffer.String()
		visible, thinking, leftover, hasMore := splitThinking(buf)

		if visible != "" {
			u.ensureMarkdownLocked()
			u.markdown.Write(u.out, visible)
			u.assistantHadVisible = true
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

// ensureMarkdownLocked lazily wires a MarkdownRenderer to the TUI. Called
// from streaming entry points so non-NewPlain constructors (tests with
// PlainUI{...}) and zero-value safety paths still get rich output without
// each test having to know to create one.
func (u *PlainUI) ensureMarkdownLocked() {
	if u.markdown == nil {
		u.markdown = NewMarkdownRenderer(u.style)
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
		u.ensureMarkdownLocked()
		u.markdown.Write(u.out, rest)
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
	}
	u.assistantReplyStart = time.Time{}
	u.assistantHadVisible = false
	if u.streamingAssistant {
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

	u.endAssistantLineLocked()
	u.closeThinkingLocked(false)
	u.flushPendingToolLocked()

	compact := CompactToolHeader(name, params)
	u.pendingTool = name
	u.pendingHeader = u.style.BoldBlue(compact)
	u.pendingActive = true

	enabled := u.style.Enabled()
	u.mu.Unlock()

	if !enabled {
		return
	}
	label := fmt.Sprintf("%s %s",
		u.style.BoldBlue(compact),
		u.style.DimBlue("running…"),
	)
	u.startSpinner(label)
}

// ShowToolResult collapses the in-progress tool card into a final state,
// printing a one-line summary of the output rather than dumping everything.
// Long outputs are truncated; the user sees the same "✓ Read foo.go (1.2KB)"
// style that Cursor uses. For Write/Edit, toolParams enables Cursor-like
// +lines/−lines stats and a short diff preview (Edit only).
func (u *PlainUI) ShowToolResult(name, output string, isError bool, toolParams map[string]any) {
	u.stopSpinner()
	u.mu.Lock()
	defer u.mu.Unlock()

	icon := u.style.BrightGreen(iconTool)
	tag := u.style.DimGreen("done")
	if isError {
		icon = u.style.BoldRed(iconErr)
		tag = u.style.Red("error")
	}

	header := u.pendingHeader
	if !u.pendingActive || u.pendingTool != name {
		header = u.style.BoldBlue(name)
	}

	summary := toolResultSummary(u.style, name, output, isError, toolParams)

	if u.pendingActive {
		fmt.Fprint(u.out, u.style.ClearPendingLine())
	}
	arrow := u.style.DimGreen("→ ")
	if isError {
		fmt.Fprintf(u.out, "%s %s %s %s%s\n", icon, header, tag, arrow, u.style.Red(summary))
	} else {
		fmt.Fprintf(u.out, "%s %s %s %s%s\n", icon, header, tag, arrow, summary)
	}

	if !isError && name == "Edit" && toolParams != nil {
		printEditDiffPreview(u.out, u.style, toolParams)
	}

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
	u.endAssistantLineLocked()
	if u.style.Enabled() {
		userIcon := u.style.BrightGreen(iconUser)
		userLabel := u.style.BoldGreen("user")
		promptLabel := u.style.BoldGreen(prompt)
		if u.planMode {
			userIcon = u.style.Yellow(iconUser)
			userLabel = u.style.BoldYellow("user")
			promptLabel = u.style.BoldYellow(prompt)
		}
		_, _ = io.WriteString(u.out, fmt.Sprintf("%s %s %s",
			userIcon,
			userLabel,
			promptLabel,
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

// AskPermission prompts the user with a colored panel for tool consent (English labels).
func (u *PlainUI) AskPermission(tool string, params map[string]any) Decision {
	u.stopSpinner()
	u.mu.Lock()
	u.flushPendingToolLocked()

	st := u.style
	payload := permissionPayloadLines(tool, params)

	var b strings.Builder
	b.WriteString("\n")
	for _, raw := range payload {
		line := fitGateLine(raw, permissionDisplayMaxRunes)
		b.WriteString(st.Dim("  "))
		switch {
		case strings.HasPrefix(line, "TARGET"):
			b.WriteString(st.BoldCyan(line))
		case line == "PAYLOAD":
			b.WriteString(st.DimGreen("— "))
			b.WriteString(st.BrightGreen(line))
		case strings.HasPrefix(line, "+ "):
			b.WriteString(st.Green(line))
		case strings.HasPrefix(line, "- "):
			b.WriteString(st.Red(line))
		case strings.HasPrefix(line, "…"):
			b.WriteString(st.Dim(line))
		case line == "patch:" || line == "preview:":
			b.WriteString(st.Yellow(line))
		case strings.HasPrefix(line, "file:") || strings.HasPrefix(line, "change:") || strings.HasPrefix(line, "size:"):
			b.WriteString(st.Yellow(line))
		case strings.HasSuffix(line, ":") && !strings.HasPrefix(line, " "):
			b.WriteString(st.Yellow(line))
		default:
			b.WriteString(st.Dim(line))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(st.Dim("  "))
	b.WriteString(st.DimGreen("Choose"))
	b.WriteString(st.Dim(":\n"))

	writeOpt := func(n string, label, desc string, color func(string) string) {
		b.WriteString(st.Dim("      "))
		b.WriteString(st.BoldBrightGreen("[" + n + "] "))
		b.WriteString(color(label))
		if desc != "" {
			b.WriteString(st.Dim("  // " + desc))
		}
		b.WriteString("\n")
	}
	writeOpt("1", "Allow once", "this invocation only", st.BrightGreen)
	writeOpt("2", "Always allow (this session)", "until you exit vibe-coder", st.Green)
	writeOpt("3", "Always allow (saved)", "written to permissions file", st.BrightBlue)
	b.WriteString("\n")
	writeOpt("4", "Not now", "deny once; you may be asked again", st.Yellow)
	writeOpt("5", "No — block for this session", "this tool stays off until exit", st.Red)
	writeOpt("6", "Never allow (saved)", "written to permissions file", st.BoldRed)
	writeOpt("7", "Cancel", "abort this run", st.Magenta)
	b.WriteString("\n")
	b.WriteString(st.Dim("      "))
	b.WriteString(st.DimGreen("[s]"))
	b.WriteString(st.Dim("  yes_mode  "))
	b.WriteString(st.Dim("(auto-approve non-dangerous tools)"))
	b.WriteString("\n")
	b.WriteString("\n")
	b.WriteString(st.Dim("  ;; "))
	b.WriteString(st.DimGreen("stdin"))
	b.WriteString(st.Dim(" › 1–7 | "))
	b.WriteString(st.DimGreen("y"))
	b.WriteString(st.Dim("/"))
	b.WriteString(st.DimGreen("a"))
	b.WriteString(st.Dim("/"))
	b.WriteString(st.DimGreen("d"))
	b.WriteString(st.Dim(" … "))
	b.WriteString(st.BoldBrightGreen("▸ "))

	_, _ = io.WriteString(u.out, b.String())
	u.mu.Unlock()

	line, err := u.reader.ReadString('\n')
	if err != nil {
		return DecisionDenyOnce
	}
	s := strings.TrimSpace(strings.ToLower(trimLine(line)))
	if s == "" {
		return DecisionDenyOnce
	}

	switch s {
	case "1", "y", "yes":
		return DecisionAllowOnce
	case "2":
		return DecisionAllowSession
	case "3", "a", "all":
		return DecisionAllowPersistent
	case "4", "n":
		return DecisionDenyOnce
	case "5":
		return DecisionDenySession
	case "6", "d", "deny-all", "denyall":
		return DecisionDenyPersistent
	case "7", "q", "quit", "c", "cancel":
		return DecisionCancel
	case "s", "skip", "skip-all-confirm":
		return DecisionYesMode
	default:
		return DecisionDenyOnce
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

// writeThinkingChunkLocked handles in-band <think>...</think> sections that
// some models emit inside the assistant stream (e.g. when the native Ollama
// "thinking" field is unavailable). It uses the same `│` bar prefix as
// StreamThinking so the user sees a consistent reasoning panel regardless
// of how the model surfaces its reasoning, and so EndAssistant's "thought
// for Xs" footer reads as the natural close of either source.
func (u *PlainUI) writeThinkingChunkLocked(text string) {
	if !u.thinkingActive {
		fmt.Fprintf(u.out, "\n%s ", u.style.Dim(iconBar))
		u.thinkingActive = true
		u.thinkingStart = time.Now()
	}
	indented := strings.ReplaceAll(text, "\n", "\n"+iconBar+" ")
	fmt.Fprint(u.out, u.style.Dim(indented))
}

func (u *PlainUI) endAssistantLineLocked() {
	if !u.streamingAssistant {
		return
	}
	fmt.Fprintln(u.out)
	u.streamingAssistant = false
}

func (u *PlainUI) closeThinkingLocked(withElapsed bool) {
	if !u.thinkingActive {
		return
	}
	if withElapsed {
		elapsed := formatElapsed(time.Since(u.thinkingStart))
		fmt.Fprintf(u.out, "\n%s %s\n",
			u.style.Dim(iconRule),
			u.style.Dim("thought for "+elapsed),
		)
	} else {
		fmt.Fprintln(u.out)
	}
	u.thinkingActive = false
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

var _ UI = (*PlainUI)(nil)
