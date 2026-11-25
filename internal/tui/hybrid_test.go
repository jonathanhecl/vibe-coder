package tui

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestNewFromModeSupportsHybridAndAliases(t *testing.T) {
	cases := []struct {
		mode string
		want string
	}{
		{"", "*tui.PlainUI"},
		{"plain", "*tui.PlainUI"},
		{"cli", "*tui.PlainUI"},
		{"tui", "*tui.HybridUI"},
		{"hybrid", "*tui.HybridUI"},
		{"rich", "*tui.RichUI"},
	}
	for _, tc := range cases {
		ui, err := NewFromMode(&config.Config{UI: tc.mode})
		if err != nil {
			t.Fatalf("NewFromMode(%q): %v", tc.mode, err)
		}
		defer ui.Stop()
		switch tc.want {
		case "*tui.PlainUI":
			if _, ok := ui.(*PlainUI); !ok {
				t.Fatalf("NewFromMode(%q) = %T, want PlainUI", tc.mode, ui)
			}
		case "*tui.HybridUI":
			if _, ok := ui.(*HybridUI); !ok {
				t.Fatalf("NewFromMode(%q) = %T, want HybridUI", tc.mode, ui)
			}
		case "*tui.RichUI":
			if _, ok := ui.(*RichUI); !ok {
				t.Fatalf("NewFromMode(%q) = %T, want RichUI", tc.mode, ui)
			}
		}
	}

	if _, err := NewFromMode(&config.Config{UI: "invalid"}); err == nil {
		t.Fatal("expected error for invalid UI mode")
	}
}

func TestHybridInputModelTypingPasteAndSubmit(t *testing.T) {
	m := newHybridInputModel("> ", StatusInfo{Model: "qwen3:8b", ContextPct: 42}, nil, nil, 90)
	var tm tea.Model = m

	// Type "hello", Alt+Enter newline, then paste "world\nline2", then Enter to submit.
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("world\r\nline2"), Paste: true})

	cur := tm.(hybridInputModel)
	if view := cur.View(); !strings.Contains(view, "BUILD") || !strings.Contains(view, "TUI") || !strings.Contains(view, "ctx 42%") {
		t.Fatalf("unexpected status view:\n%s", view)
	}

	tm, cmd := tm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd on Enter")
	}
	done := tm.(hybridInputModel)
	if !done.submitted || done.value() != "hello\nworld\nline2" {
		t.Fatalf("unexpected submitted value: submitted=%t val=%q", done.submitted, done.value())
	}
	if done.View() != "" {
		t.Fatalf("expected empty view after quit, got %q", done.View())
	}
}

func TestHybridInputModelSlashAutocompleteAndHistory(t *testing.T) {
	cmds := []SlashCommand{
		{Name: "/save", Description: "save settings"},
		{Name: "/status", Description: "show status"},
		{Name: "/ui", Description: "switch UI mode"},
	}
	m := newHybridInputModel("> ", StatusInfo{PlanMode: true}, []string{"first", "second"}, cmds, 80)
	var tm tea.Model = m

	// History Up/Down
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := tm.(hybridInputModel).value(); got != "second" {
		t.Fatalf("expected second from history, got %q", got)
	}
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := tm.(hybridInputModel).value(); got != "first" {
		t.Fatalf("expected first from history, got %q", got)
	}
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyDown})
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := tm.(hybridInputModel).value(); got != "" {
		t.Fatalf("expected empty draft after down, got %q", got)
	}

	// Slash filtering & Tab completion
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/s")})
	cur := tm.(hybridInputModel)
	suggs := cur.matchingSlashCommands()
	if len(suggs) != 2 || suggs[0].Name != "/save" || suggs[1].Name != "/status" {
		t.Fatalf("unexpected matching slash suggestions: %+v", suggs)
	}
	view := cur.View()
	if !strings.Contains(view, "/save") || !strings.Contains(view, "/status") {
		t.Fatalf("unexpected slash suggestions view:\n%s", view)
	}
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyDown}) // move to /status
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := tm.(hybridInputModel).value(); got != "/status " {
		t.Fatalf("expected Tab completion '/status ', got %q", got)
	}
}

func TestHybridInputModelCtrlCandCtrlD(t *testing.T) {
	m := newHybridInputModel("> ", StatusInfo{}, nil, nil, 80)
	var tm tea.Model = m

	// Ctrl+C on non-empty buffer clears buffer.
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abc")})
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if got := tm.(hybridInputModel).value(); got != "" {
		t.Fatalf("expected Ctrl+C to clear buffer, got %q", got)
	}

	// Ctrl+C on empty buffer sets notice instead of quitting.
	tm, cmd := tm.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil || !strings.Contains(tm.(hybridInputModel).notice, "Ctrl+D") {
		t.Fatalf("expected Ctrl+D notice on empty Ctrl+C, got notice=%q cmd=%v", tm.(hybridInputModel).notice, cmd)
	}

	// Ctrl+D on empty buffer triggers EOF quit.
	tm, cmd = tm.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd == nil || !tm.(hybridInputModel).eof {
		t.Fatalf("expected EOF quit on empty Ctrl+D")
	}
}

func TestHybridPermissionModelNavigationAndTimeout(t *testing.T) {
	m := newHybridPermissionModel([]string{"ACTION Edit", "FILE: a.go"}, 5*time.Second, 80)
	var tm tea.Model = m
	if view := tm.(hybridPermissionModel).View(); !strings.Contains(view, "PERMISSION REQUIRED") || !strings.Contains(view, "Allow once") {
		t.Fatalf("unexpected permission view:\n%s", view)
	}

	// Down arrow moves from Allow once (0) -> Allow this session (1), Enter confirms.
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyDown})
	tm, cmd := tm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || tm.(hybridPermissionModel).decision != DecisionAllowSession {
		t.Fatalf("expected DecisionAllowSession, got %v", tm.(hybridPermissionModel).decision)
	}

	// Direct hotkey "6" -> DecisionDenyPersistent.
	m2 := newHybridPermissionModel([]string{"TARGET Bash"}, 0, 80)
	tm2, cmd2 := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("6")})
	if cmd2 == nil || tm2.(hybridPermissionModel).decision != DecisionDenyPersistent {
		t.Fatalf("expected DecisionDenyPersistent, got %v", tm2.(hybridPermissionModel).decision)
	}

	// Tick past deadline -> DecisionTimeout.
	m3 := newHybridPermissionModel([]string{"TARGET Bash"}, time.Second, 80)
	tm3, cmd3 := m3.Update(hybridTickMsg(m3.deadline.Add(time.Millisecond)))
	if cmd3 == nil || !tm3.(hybridPermissionModel).timedOut || tm3.(hybridPermissionModel).decision != DecisionTimeout {
		t.Fatalf("expected DecisionTimeout on expired tick, got %+v", tm3.(hybridPermissionModel))
	}
}

func TestHybridAskModelSelectionCustomAndEscape(t *testing.T) {
	card := AskCard{
		Title:  "Database",
		Prompt: "Which DB?",
		Options: []AskOption{
			{Label: "SQLite", Description: "embedded"},
			{Label: "Postgres", Description: "server"},
		},
	}

	// Arrow + Enter picks option "2".
	m := newHybridAskModel(card, 0, 80)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyDown})
	tm, cmd := tm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || tm.(hybridAskModel).answer != "2" {
		t.Fatalf("expected answer '2', got %q", tm.(hybridAskModel).answer)
	}

	// Custom answer mode via 'c'.
	m2 := newHybridAskModel(card, 0, 80)
	var tm2 tea.Model = m2
	tm2, _ = tm2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	tm2, _ = tm2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("DuckDB")})
	tm2, cmd2 := tm2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd2 == nil || tm2.(hybridAskModel).answer != "DuckDB" {
		t.Fatalf("expected custom answer 'DuckDB', got %q", tm2.(hybridAskModel).answer)
	}

	// Esc sets escaped=true.
	m3 := newHybridAskModel(card, 0, 80)
	tm3, cmd3 := m3.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd3 == nil || !tm3.(hybridAskModel).escaped {
		t.Fatal("expected escaped=true on Esc")
	}
}

func TestHybridUINonTTYDelegationAndState(t *testing.T) {
	var out bytes.Buffer
	plain := &PlainUI{
		out:    &out,
		reader: bufio.NewReader(strings.NewReader("hello from pipe\n")),
		stopCh: make(chan struct{}),
		history: &inputHistory{},
	}
	h := &HybridUI{plain: plain, status: StatusInfo{ContextPct: -1}}
	h.SetPlanMode(true)
	h.ShowTodos([]TodoItem{
		{ID: "1", Content: "one", Status: todoStatusCompleted},
		{ID: "2", Content: "two", Status: todoStatusPending},
	})
	h.SetSlashCommands([]SlashCommand{{Name: "/ui", Description: "switch"}})

	if !h.status.PlanMode || h.status.TodosDone != 1 || h.status.TodosTotal != 2 {
		t.Fatalf("unexpected hybrid status: %+v", h.status)
	}

	got, err := h.GetInput("> ")
	if err != nil || got != "hello from pipe" {
		t.Fatalf("expected pipe fallback input, got %q err=%v", got, err)
	}
}

func newStyledHybridForTest(out *bytes.Buffer) *HybridUI {
	st := NewStyleForTest(true)
	plain := &PlainUI{
		out:      out,
		outIsTTY: true,
		style:    st,
		markdown: NewRichMarkdownRenderer(st),
		stopCh:   make(chan struct{}),
		history:  &inputHistory{},
	}
	return &HybridUI{
		plain:  plain,
		status: StatusInfo{ContextPct: -1},
	}
}

func TestHybridThinkingBlockStreamingAndWrap(t *testing.T) {
	var out bytes.Buffer
	h := newStyledHybridForTest(&out)

	// Stream a line that previously would split mid-word ("i\nn kind"), plus >6 lines
	// to verify bounded live scrolling and right-border alignment.
	h.StreamThinking("The user said \"hola,\" which is Spanish for \"hello.\" This is a simple greeting. I should respond in kind, matching their language.\n")
	h.StreamThinking("Line 3\nLine 4\nLine 5\nLine 6\nLine 7\nLine 8\nLine 9")
	h.EndThinking()

	got := out.String()
	// Extract the final settled frame after the last clear-screen sequence.
	finalFrame := got
	if idx := strings.LastIndex(got, "\x1b[J"); idx >= 0 {
		finalFrame = got[idx+len("\x1b[J"):]
	}

	if !strings.Contains(finalFrame, "╭─ 💭 Thinking (lines ") {
		t.Fatalf("expected thinking block header with line window, got:\n%s", finalFrame)
	}
	if !strings.Contains(finalFrame, "╰─") || !strings.Contains(finalFrame, "thought for") || !strings.Contains(finalFrame, "Ctrl+T to scroll") {
		t.Fatalf("expected thinking block footer with duration and scroll hint, got:\n%s", finalFrame)
	}
	if strings.Contains(finalFrame, "Line 3") {
		t.Fatalf("expected older lines to scroll out of the 6-line window, got:\n%s", finalFrame)
	}
	if !strings.Contains(finalFrame, "Line 9") {
		t.Fatalf("expected latest line to stay visible inside the 6-line window, got:\n%s", finalFrame)
	}

	// Verify closed rounded borders and uniform exact display width on every row.
	rows := strings.Split(strings.Trim(finalFrame, "\n"), "\n")
	if len(rows) != hybridThinkMaxLines+2 {
		t.Fatalf("expected %d rows (%d content + 2 borders), got %d:\n%s", hybridThinkMaxLines+2, hybridThinkMaxLines, len(rows), finalFrame)
	}
	expectedWidth := displayWidth(rows[0])
	if !strings.HasSuffix(rows[0], "╮") || !strings.HasSuffix(rows[len(rows)-1], "╯") {
		t.Fatalf("expected closed right corners ╮ and ╯, got:\n%s", finalFrame)
	}
	for i, row := range rows {
		if w := displayWidth(row); w != expectedWidth {
			t.Fatalf("row %d width mismatch: got %d, want %d\nrow=%q", i, w, expectedWidth, row)
		}
	}

	// Verify word wrapping never splits "in kind" across lines.
	wrapped := wrapThinkingLines("The user said \"hola,\" which is Spanish for \"hello.\" I should respond in kind, matching their language.", 74)
	if len(wrapped) < 2 {
		t.Fatalf("expected wrapped paragraph, got %v", wrapped)
	}
	if !strings.Contains(strings.Join(wrapped, " "), "in kind") {
		t.Fatalf("unexpected wrapped output: %v", wrapped)
	}
	for _, wl := range wrapped {
		if strings.HasSuffix(wl, " i") || strings.HasPrefix(wl, "n ") {
			t.Fatalf("word 'in' split across wrap boundary: %v", wrapped)
		}
	}
}

func TestHybridThinkingViewerScrolling(t *testing.T) {
	var lines []string
	for i := 1; i <= 12; i++ {
		lines = append(lines, fmt.Sprintf("Thought step %02d", i))
	}
	raw := strings.Join(lines, "\n")

	m := newHybridThinkingViewerModel(raw, 80, 14) // viewHeight = 8
	v0 := m.View()
	if !strings.Contains(v0, "lines 1-8 of 12") || !strings.Contains(v0, "Thought step 01") || strings.Contains(v0, "Thought step 09") {
		t.Fatalf("unexpected initial viewer render:\n%s", v0)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(hybridThinkingViewerModel)
	v1 := m.View()
	if !strings.Contains(v1, "lines 2-9 of 12") || !strings.Contains(v1, "Thought step 09") {
		t.Fatalf("expected KeyDown to scroll to lines 2-9, got:\n%s", v1)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = next.(hybridThinkingViewerModel)
	vEnd := m.View()
	if !strings.Contains(vEnd, "lines 5-12 of 12") || !strings.Contains(vEnd, "Thought step 12") {
		t.Fatalf("expected KeyEnd to scroll to bottom, got:\n%s", vEnd)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = next.(hybridThinkingViewerModel)
	if m.offset != 0 {
		t.Fatalf("expected KeyHome to return offset to 0, got %d", m.offset)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(hybridThinkingViewerModel)
	if !m.quitting || cmd == nil {
		t.Fatalf("expected KeyEsc to quit viewer")
	}

	// Verify Ctrl+T in composer sets inspectThinking only when thinking is available
	// and preserves the typed draft.
	inNoThink := newHybridInputModel("> ", StatusInfo{ContextPct: -1}, nil, nil, 80)
	nextIn, _ := inNoThink.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	inNoThink = nextIn.(hybridInputModel)
	if inNoThink.inspectThinking || inNoThink.notice == "" {
		t.Fatalf("expected Ctrl+T without thinking to show notice and stay active, got %+v", inNoThink)
	}

	inWithThink := newHybridInputModel("> ", StatusInfo{ContextPct: -1}, nil, nil, 80).
		withThinkingAvailable(true).
		withDraft([]rune("draft prompt"), 5)
	if !strings.Contains(inWithThink.View(), "Ctrl+T think") {
		t.Fatalf("expected help bar to advertise Ctrl+T think, got:\n%s", inWithThink.View())
	}
	nextIn, cmd = inWithThink.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	inWithThink = nextIn.(hybridInputModel)
	if !inWithThink.inspectThinking || !inWithThink.quitting || cmd == nil || string(inWithThink.buf) != "draft prompt" || inWithThink.cursor != 5 {
		t.Fatalf("expected Ctrl+T with thinking to quit for viewer while keeping draft, got %+v", inWithThink)
	}
}

func TestHybridInBandThinkTagsRenderInBlock(t *testing.T) {
	var out bytes.Buffer
	h := newStyledHybridForTest(&out)
	h.StartTurn()

	h.StreamAssistant("<think>checking files</think>Here is the answer.\n")
	h.EndAssistant(TurnUsage{CompletionTokens: 20, EvalDuration: 500 * time.Millisecond})

	got := out.String()
	if !strings.Contains(got, "╭─ 💭 Thinking") || !strings.Contains(got, "checking files") || !strings.Contains(got, "thought for") {
		t.Fatalf("expected in-band <think> rendered inside thinking block, got:\n%s", got)
	}
	if !strings.Contains(got, "● assistant") || !strings.Contains(got, "Here is the answer.") {
		t.Fatalf("expected assistant header and body, got:\n%s", got)
	}
	if !strings.Contains(got, "responded in") || !strings.Contains(got, "40.0 tok/s") {
		t.Fatalf("expected turn footer with speed, got:\n%s", got)
	}
}

func TestHybridToolResultCardWithDiffAndError(t *testing.T) {
	var out bytes.Buffer
	h := newStyledHybridForTest(&out)

	h.ShowToolResult("Edit", "updated", false, map[string]any{
		"file_path":  "internal/app.go",
		"old_string": "a := 1",
		"new_string": "a := 2",
		"_diff":      "@@ -1,1 +1,1 @@\n-a := 1\n+a := 2\n",
	})
	h.ShowToolResult("Bash", "exit status 1\ntest failed", true, map[string]any{
		"command": "go test ./...",
	})

	got := out.String()
	if !strings.Contains(got, "✓") || !strings.Contains(got, "Edit") || !strings.Contains(got, "+a := 2") {
		t.Fatalf("expected styled Edit tool card with diff, got:\n%s", got)
	}
	if !strings.Contains(got, "✗") || !strings.Contains(got, "error") || !strings.Contains(got, "test failed") {
		t.Fatalf("expected styled Bash error card, got:\n%s", got)
	}
}

func TestHybridTodosPanelWithProgressBar(t *testing.T) {
	var out bytes.Buffer
	h := newStyledHybridForTest(&out)

	h.ShowTodos([]TodoItem{
		{ID: "1", Content: "Read code", Status: todoStatusCompleted},
		{ID: "2", Content: "Update UI", Status: todoStatusInProgress},
		{ID: "3", Content: "Run tests", Status: todoStatusPending},
	})

	got := out.String()
	if !strings.Contains(got, "To-dos") || !strings.Contains(got, "1/3") {
		t.Fatalf("expected To-dos header with 1/3 progress, got:\n%s", got)
	}
	if !strings.Contains(got, "█") || !strings.Contains(got, "░") {
		t.Fatalf("expected progress bar glyphs, got:\n%s", got)
	}
	if !strings.Contains(got, "╭") || !strings.Contains(got, "╯") {
		t.Fatalf("expected rounded panel border, got:\n%s", got)
	}
}

func TestHybridStyleDisabledFallsBackToPlain(t *testing.T) {
	var out bytes.Buffer
	plain := &PlainUI{
		out:      &out,
		style:    NewStyleForTest(false),
		markdown: NewMarkdownRenderer(NewStyleForTest(false)),
		stopCh:   make(chan struct{}),
	}
	h := &HybridUI{plain: plain, status: StatusInfo{ContextPct: -1}}

	h.StreamThinking("plain thought\n")
	h.EndThinking()
	h.ShowTodos([]TodoItem{{ID: "1", Content: "Step 1", Status: todoStatusPending}})

	got := out.String()
	if strings.Contains(got, "╭─") || strings.Contains(got, "● assistant") {
		t.Fatalf("non-styled output should fall back to PlainUI formatting, got:\n%s", got)
	}
	if !strings.Contains(got, "plain thought") || !strings.Contains(got, "To-dos 1") {
		t.Fatalf("missing plain content, got:\n%s", got)
	}
}
