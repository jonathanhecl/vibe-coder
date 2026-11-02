package tui

import (
	"strings"
	"testing"
	"time"
)

// A shell card reads like a log line: the shield, the tool name, the command
// in the same gray as thinking (it is context, not a result), and the verdict
// after the arrow.
func TestShowToolResultShellCard(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	u := &PlainUI{out: &b, style: Style{}}
	cmd := "cd /mnt/store/carpeta && echo \"cookie hex:\""
	u.ShowToolCall("Bash", map[string]any{"command": cmd})
	u.ShowToolResult("Bash", "line1\nline2\nline3\nline4", false, map[string]any{"command": cmd})

	got := b.String()
	if !strings.Contains(got, iconShell+" Shell $ "+cmd) {
		t.Fatalf("expected the shell header %q, got:\n%q", iconShell+" Shell $ "+cmd, got)
	}
	if strings.Contains(got, "done") {
		t.Fatalf("a successful card must not print a done tag:\n%q", got)
	}
	if !strings.Contains(got, "→ ") || !strings.Contains(got, "4 lines, 23B") {
		t.Fatalf("expected the arrow and the output summary, got:\n%q", got)
	}
}

// The command earns the wider budget: a realistic chained one-liner keeps its
// visible part instead of collapsing to an ellipsis at 52 runes.
func TestShellHeaderKeepsAHundredRunes(t *testing.T) {
	t.Parallel()
	cmd := "cd /mnt/store/carpeta && echo \"cookie hex:\" && ls -la | head -20 && df -h | grep store"
	header := toolHeader(Style{}, "Bash", map[string]any{"command": cmd})
	if !strings.HasPrefix(header, "Shell $ "+cmd[:60]) {
		t.Fatalf("the command was cut too early: %q", header)
	}
	if strings.Contains(header, "…") && len([]rune(header)) < 60 {
		t.Fatalf("header collapsed: %q", header)
	}

	long := strings.Repeat("x", 300)
	clipped := toolHeader(Style{}, "Bash", map[string]any{"command": long})
	if !strings.HasSuffix(clipped, "…") {
		t.Fatalf("a 300-rune command must be clipped: %q", clipped)
	}
	if n := len([]rune(clipped)); n > bashHeaderRunes {
		t.Fatalf("clipped header has %d runes, want at most %d", n, bashHeaderRunes)
	}
}

// The card reports the command's own duration, because the card opens after the
// permission prompt: the wait is not the tool's.
func TestShowToolResultShellReportsDuration(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	u := &PlainUI{out: &b, style: Style{}}
	u.ShowToolCall("Bash", map[string]any{"command": "sleep 0.3"})
	// The wall clock cannot be faked without sleeping, so pin the start.
	u.pendingStart = time.Now().Add(-300 * time.Millisecond)
	u.ShowToolResult("Bash", "out", false, map[string]any{"command": "sleep 0.3"})

	if got := b.String(); !strings.Contains(got, "0.3s,") {
		t.Fatalf("expected the duration before the summary, got:\n%q", got)
	}
}

// The name carries the tool and the path keeps its color: the green marks what
// ran, the blue marks where.
func TestToolHeaderColorsTheNameAndThePath(t *testing.T) {
	t.Parallel()
	st := NewStyleForTest(true)
	header := toolHeader(st, "Edit", map[string]any{"file_path": "/home/u/proyecto/carpeta/archivo.go"})

	if !strings.HasPrefix(header, st.BoldGreen("Edit")+" ") {
		t.Fatalf("the tool name must be bold green: %q", header)
	}
	if !strings.Contains(header, st.BoldBlue("carpeta/archivo.go")) {
		t.Fatalf("the path must stay blue: %q", header)
	}
	shell := toolHeader(st, "Bash", map[string]any{"command": "ls"})
	if !strings.HasPrefix(shell, st.BoldGreen("Shell")+" ") {
		t.Fatalf("the shell name must be bold green: %q", shell)
	}
	if !strings.Contains(shell, st.Gray("$ ls")) {
		t.Fatalf("the command must use the thinking gray: %q", shell)
	}
}

// The diff stat reads as one number pair, the same way the label counts a
// delete: +N in lime and −N lines in red, then the byte delta in lime.
func TestEditChipIsOneNumberPair(t *testing.T) {
	t.Parallel()
	st := NewStyleForTest(true)
	chip := toolResultSummary(st, "Edit", "Write successful.", false, map[string]any{
		"old_string": strings.Repeat("a\n", 7), // 8 lines
		"new_string": strings.Repeat("b\n", 5), // 6 lines
	})
	if !strings.Contains(chip, st.BrightGreen("+6")+st.Dim("/")+st.Red("−8")+" lines") {
		t.Fatalf("unexpected edit chip: %q", chip)
	}
	write := toolResultSummary(st, "Write", "Write successful.", false, map[string]any{
		"contents": "a\nb\nc",
	})
	if !strings.Contains(write, st.BrightGreen("+3")+" lines") {
		t.Fatalf("unexpected write chip: %q", write)
	}

	// A shrink is not growth: the byte delta turns red with the removed lines.
	shrink := toolResultSummary(st, "Edit", "Write successful.", false, map[string]any{
		"old_string": strings.Repeat("a\n", 9),
		"new_string": strings.Repeat("b\n", 3),
	})
	if !strings.Contains(shrink, st.Red("−12B")) {
		t.Fatalf("a shrinking edit must report the byte delta in red: %q", shrink)
	}
}

// A web card shows its whole target: a truncated URL cannot be checked or
// re-opened, and the query is the only thing that says what was searched.
func TestWebCardsShowTheWholeTarget(t *testing.T) {
	t.Parallel()
	st := NewStyleForTest(true)
	url := "https://example.com/docs/" + strings.Repeat("segmento/", 20) + "?page=2&q=" + strings.Repeat("x", 40)
	header := toolHeader(st, "WebFetch", map[string]any{"url": url})
	if !strings.HasPrefix(header, st.BoldGreen("WebFetch")+" ") {
		t.Fatalf("the tool name must be bold green: %q", header)
	}
	if !strings.Contains(header, st.Gray(url)) {
		t.Fatalf("the whole URL must be shown in gray: %q", header)
	}
	if strings.Contains(header, "…") {
		t.Fatalf("a URL must not be clipped on the final card: %q", header)
	}

	query := "cómo consultar el clima de GCHQ Global Climate Change Hub con ejemplos y varias palabras"
	search := toolHeader(st, "WebSearch", map[string]any{"query": query})
	if !strings.Contains(search, st.Gray(query)) {
		t.Fatalf("the whole query must be shown in gray: %q", search)
	}

	// The animated line is the exception: it must still fit one row.
	label := pendingLabel(NewStyleForTest(false), "WebFetch", map[string]any{"url": url})
	if n := len([]rune(label)); n > maxToolHeaderLen {
		t.Fatalf("pending label has %d runes, want at most %d: %q", n, maxToolHeaderLen, label)
	}
	if !strings.HasSuffix(label, "… running…") {
		t.Fatalf("the clipped URL must end in an ellipsis: %q", label)
	}
}
