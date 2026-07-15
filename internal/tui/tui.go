package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"golang.org/x/term"
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
	CollapseAssistantOutput()
}

// PlainUI renders an interactive session in a way inspired by Cursor's chat:
// streamed assistant text, a single-line tool "card" that gets rewritten in
// place when the result arrives, and dim "thinking" sections.
//
// PlainUI uses raw mode only while reading interactive input so bracketed paste
// can be rendered as a compact block instead of being echoed in full. The
// reader restores the terminal immediately after Enter, paste completion, or
// Ctrl+C; the rest of the UI continues to use buffered stdin reads.
type PlainUI struct {
	in  *os.File
	out io.Writer

	reader              *bufio.Reader
	style               Style
	planMode            bool
	bracketedPaste      bool
	restoreTerminalMode func()
	cfg                 *config.Config

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
	turnStart           time.Time
	assistantHadVisible bool
	assistantLines      int
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
func NewPlain(cfg ...*config.Config) *PlainUI {
	var c *config.Config
	if len(cfg) > 0 {
		c = cfg[0]
	}
	st := NewStyle(os.Stdout)
	u := &PlainUI{
		in:        os.Stdin,
		out:       os.Stdout,
		reader:    bufio.NewReader(os.Stdin),
		style:     st,
		stopCh:    make(chan struct{}),
		markdown:  NewMarkdownRenderer(st),
		cfg:       c,
		turnStart: time.Now(),
	}
	if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		if restore, ok := configureTerminalForBracketedPaste(os.Stdin, os.Stdout); ok {
			fmt.Fprint(u.out, enableBracketedPaste)
			u.bracketedPaste = true
			u.restoreTerminalMode = restore
		}
	}
	return u
}

// SetPlanMode switches UI accent colors for role labels between plan/build.
func (u *PlainUI) SetPlanMode(enabled bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.planMode = enabled
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
		if u.bracketedPaste {
			fmt.Fprint(u.out, disableBracketedPaste)
		}
		if u.restoreTerminalMode != nil {
			u.restoreTerminalMode()
		}
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

// writeThinkingChunkLocked handles in-band <thinking>...</thinking> sections that
// some models emit inside the assistant stream (e.g. when the native Ollama
// "thinking" field is unavailable). It uses the same `│` bar prefix as
// StreamThinking so the user sees a consistent reasoning panel regardless
// of how the model surfaces its reasoning, and so EndAssistant's "thought
// for Xs" footer reads as the natural close of either source.
func (u *PlainUI) writeThinkingChunkLocked(text string) {
	if u.cfg != nil && u.cfg.OllamaHideThink {
		return
	}
	if !u.thinkingActive {
		fmt.Fprintf(u.out, "\n%s ", u.style.Dim(iconBar))
		u.thinkingActive = true
		u.thinkingStart = time.Now()
	}
	indented := strings.ReplaceAll(text, "\n", "\n"+iconBar+" ")
	fmt.Fprint(u.out, u.style.Dim(indented))
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

var _ UI = (*PlainUI)(nil)
