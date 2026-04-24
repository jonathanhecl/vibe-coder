package tui

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRenderTodosPlain(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	renderTodos(&buf, Style{}, []TodoItem{
		{ID: "a", Content: "Read main.go", Status: "completed"},
		{ID: "b", Content: "Run tests", Status: "in_progress"},
		{ID: "c", Content: "Open PR", Status: "pending"},
		{ID: "d", Content: "Deprecated step", Status: "cancelled"},
	})
	out := buf.String()

	if !strings.Contains(out, "To-dos 4") {
		t.Fatalf("missing header: %q", out)
	}
	for _, want := range []string{
		"✓  Read main.go",
		"◐  Run tests",
		"○  Open PR",
		"✗  Deprecated step",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestShowTodosNoOpOnEmpty(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	ui := &PlainUI{out: &buf, style: Style{}, stopCh: make(chan struct{})}
	ui.ShowTodos(nil)
	ui.ShowTodos([]TodoItem{})
	if buf.Len() != 0 {
		t.Fatalf("ShowTodos must not print for empty list, got %q", buf.String())
	}
}

func TestRenderTodosFallsBackToIDWhenContentEmpty(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	renderTodos(&buf, Style{}, []TodoItem{
		{ID: "step-1", Content: "", Status: "pending"},
		{ID: "step-2", Content: "   ", Status: "in_progress"},
	})
	out := buf.String()
	if !strings.Contains(out, "○  step-1") {
		t.Fatalf("missing fallback for empty content: %q", out)
	}
	if !strings.Contains(out, "◐  step-2") {
		t.Fatalf("missing fallback for whitespace-only content: %q", out)
	}
}

func TestCompactToolHeaderPerTool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		tool   string
		params map[string]any
		want   string
	}{
		{
			name:   "Read absolute path",
			tool:   "Read",
			params: map[string]any{"file_path": "/home/user/proj/internal/agent/loop.go"},
			want:   "Read agent/loop.go",
		},
		{
			name:   "Read windows path",
			tool:   "Read",
			params: map[string]any{"file_path": `C:\proj\pkg\foo.go`},
			want:   "Read pkg/foo.go",
		},
		{
			name:   "Read relative",
			tool:   "Read",
			params: map[string]any{"file_path": "AGENTS.md"},
			want:   "Read AGENTS.md",
		},
		{
			name:   "Glob with pattern only",
			tool:   "Glob",
			params: map[string]any{"pattern": "**/*.go"},
			want:   "Glob **/*.go",
		},
		{
			name:   "Glob with target dir",
			tool:   "Glob",
			params: map[string]any{"pattern": "*.go", "target_directory": "/abs/internal/tui"},
			want:   "Glob *.go in internal/tui",
		},
		{
			name:   "Grep with path",
			tool:   "Grep",
			params: map[string]any{"pattern": "TODO", "path": "/abs/foo"},
			want:   `Grep "TODO" in abs/foo`,
		},
		{
			name:   "Grep without path",
			tool:   "Grep",
			params: map[string]any{"pattern": "needle"},
			want:   `Grep "needle"`,
		},
		{
			name:   "Bash truncated",
			tool:   "Bash",
			params: map[string]any{"command": "go test ./..."},
			want:   "Bash $ go test ./...",
		},
		{
			name:   "TodoWrite count",
			tool:   "TodoWrite",
			params: map[string]any{"todos": []any{1, 2, 3}},
			want:   "TodoWrite (3 items)",
		},
		{
			name:   "WebFetch url",
			tool:   "WebFetch",
			params: map[string]any{"url": "https://example.com/path"},
			want:   "WebFetch https://example.com/path",
		},
		{
			name:   "Unknown tool falls back to params",
			tool:   "Unknown",
			params: map[string]any{"foo": "bar"},
			want:   "Unknown(foo=bar)",
		},
		{
			name:   "Unknown tool no params",
			tool:   "Unknown",
			params: nil,
			want:   "Unknown",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CompactToolHeader(tc.tool, tc.params)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCompactPathWindowsAndPosix(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                  "",
		"foo.go":            "foo.go",
		"./foo.go":          "./foo.go",
		"a/b/c/d.go":        "c/d.go",
		"/abs/single.go":    "abs/single.go",
		`C:\one\two\x.go`:   "two/x.go",
		`C:\proj\pkg\x.go/`: "pkg/x.go",
	}
	for in, want := range cases {
		if got := compactPath(in); got != want {
			t.Fatalf("compactPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompactToolHeaderIsBoundedForSpinner(t *testing.T) {
	t.Parallel()
	got := CompactToolHeader("WebSearch", map[string]any{
		"query": "cómo consultar el clima de GCHQ Global Climate Change Hub con ejemplos y varias palabras extras",
	})
	if utf8.RuneCountInString(got) > 52 {
		t.Fatalf("header too long (%d runes): %q", utf8.RuneCountInString(got), got)
	}
	if !strings.HasPrefix(got, "WebSearch ") {
		t.Fatalf("unexpected header prefix: %q", got)
	}
}
