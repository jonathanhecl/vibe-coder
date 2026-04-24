package tui

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// TodoItem mirrors tools.TodoItem without importing the tools package, so
// the TUI stays free of feature dependencies. The agent does the cheap
// conversion when calling ShowTodos.
type TodoItem struct {
	ID      string
	Content string
	Status  string // pending | in_progress | completed | cancelled
}

const (
	todoStatusPending    = "pending"
	todoStatusInProgress = "in_progress"
	todoStatusCompleted  = "completed"
	todoStatusCancelled  = "cancelled"
)

// ShowTodos paints a Cursor-style "To-dos" panel with the current list,
// using glyphs and colors that match the spinner/thinking aesthetic.
//
// The panel is single-shot: every call repaints from scratch. Cursor's UI
// updates the list in place by redrawing it after each TodoWrite call,
// and we mirror that by appending a fresh block — terminals don't let us
// move the cursor up safely once other output (assistant streaming, tool
// cards) has been printed in between.
func (u *PlainUI) ShowTodos(items []TodoItem) {
	if len(items) == 0 {
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
	if u.thinkingActive {
		fmt.Fprintln(u.out)
		u.thinkingActive = false
	}

	renderTodos(u.out, u.style, items)
}

// renderTodos is the pure-formatting half of ShowTodos. Pulled out so
// tests can drive it with a known writer and a disabled style.
func renderTodos(w io.Writer, st Style, items []TodoItem) {
	header := fmt.Sprintf("To-dos %d", len(items))
	if st.Enabled() {
		fmt.Fprintf(w, "\n%s %s\n", st.BoldBlue("▭"), st.BoldBlue(header))
	} else {
		fmt.Fprintf(w, "\n%s\n", header)
	}

	for _, it := range items {
		glyph, color := todoGlyph(st, it.Status)
		body := it.Content
		if it.Status == todoStatusCompleted && st.Enabled() {
			body = st.Dim(body)
		}
		if st.Enabled() {
			fmt.Fprintf(w, "  %s  %s\n", color(glyph), body)
		} else {
			fmt.Fprintf(w, "  %s  %s\n", glyph, body)
		}
	}
}

// todoGlyph picks the right symbol+color combo for a status. We pick
// shapes Cursor uses (○ pending, ◐ in_progress, ✓ completed, ✗ cancelled)
// so users coming from there feel at home.
func todoGlyph(st Style, status string) (string, func(string) string) {
	switch status {
	case todoStatusInProgress:
		return "◐", st.BoldYellow
	case todoStatusCompleted:
		return "✓", st.BrightGreen
	case todoStatusCancelled:
		return "✗", st.Red
	default:
		return "○", st.Dim
	}
}

// CompactToolHeader renders a Cursor-style one-line label for a tool call,
// e.g. "Read foo.go", "Glob *.go in src/", "Bash $ ls -la". Tool-specific
// formatting falls back to the generic key=value list for unknown tools.
//
// Exported so the agent loop can reuse it for non-stream paths (e.g. tool
// observation hints) without duplicating the formatting rules.
func CompactToolHeader(name string, params map[string]any) string {
	const maxHeaderLen = 52 // keeps spinner card on one terminal row
	out := ""
	switch name {
	case "Read":
		if p, ok := params["file_path"].(string); ok && p != "" {
			out = name + " " + compactPath(p)
		}
	case "Write", "Edit", "NotebookEdit":
		if p, ok := params["file_path"].(string); ok && p != "" {
			out = name + " " + compactPath(p)
		}
	case "Glob":
		pattern, _ := params["pattern"].(string)
		dir, _ := params["target_directory"].(string)
		switch {
		case pattern != "" && dir != "":
			out = fmt.Sprintf("Glob %s in %s", pattern, compactPath(dir))
		case pattern != "":
			out = "Glob " + pattern
		}
	case "Grep":
		pattern, _ := params["pattern"].(string)
		path, _ := params["path"].(string)
		switch {
		case pattern != "" && path != "":
			out = fmt.Sprintf("Grep %q in %s", pattern, compactPath(path))
		case pattern != "":
			out = fmt.Sprintf("Grep %q", pattern)
		}
	case "Bash":
		if cmd, ok := params["command"].(string); ok && cmd != "" {
			out = "Bash $ " + truncateInline(strings.TrimSpace(cmd), 80)
		}
	case "TodoWrite":
		if todos, ok := params["todos"].([]any); ok {
			out = fmt.Sprintf("TodoWrite (%d items)", len(todos))
		} else {
			out = "TodoWrite"
		}
	case "WebFetch":
		if u, ok := params["url"].(string); ok && u != "" {
			out = "WebFetch " + truncateInline(u, 60)
		}
	case "WebSearch":
		if q, ok := params["query"].(string); ok && q != "" {
			out = "WebSearch " + truncateInline(q, 60)
		}
	}
	if out == "" {
		if extra := formatParams(params); extra != "" {
			out = name + extra
		} else {
			out = name
		}
	}
	return truncateInline(out, maxHeaderLen)
}

// compactPath turns "/abs/long/path/foo.go" into "path/foo.go" so tool
// cards stay readable. Both POSIX and Windows separators are normalised to
// "/" for display because that's the form Cursor uses and most users
// recognise; the underlying tool still receives the original absolute
// path. Relative paths are returned untouched.
func compactPath(p string) string {
	if p == "" {
		return p
	}
	normal := filepath.ToSlash(strings.TrimRight(p, "/\\"))
	parts := strings.Split(normal, "/")
	if len(parts) <= 2 {
		return normal
	}
	return strings.Join(parts[len(parts)-2:], "/")
}
