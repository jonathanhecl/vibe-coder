package tui

import (
	"bufio"
	"bytes"
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

	h.StreamThinking("First line of thought.\n")
	h.StreamThinking(strings.Repeat("a", 95))
	h.EndThinking()

	got := out.String()
	if !strings.Contains(got, "╭─ 💭 Thinking") {
		t.Fatalf("expected thinking block header, got:\n%s", got)
	}
	if !strings.Contains(got, "│") {
		t.Fatalf("expected continuous left rail in thinking block, got:\n%s", got)
	}
	if !strings.Contains(got, "╰─") || !strings.Contains(got, "thought for") {
		t.Fatalf("expected thinking block footer with duration, got:\n%s", got)
	}
	// Wrapped 95-char line should produce at least 3 rail-prefixed lines (1 header text + 2 wrapped).
	if count := strings.Count(got, "│"); count < 3 {
		t.Fatalf("expected wrapped thinking lines with rail, got %d rails:\n%s", count, got)
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
