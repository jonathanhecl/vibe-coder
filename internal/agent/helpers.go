package agent

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/permissions"
)

var (
	numberedTaskPattern = regexp.MustCompile(`(?m)^\s*(\d+[\.\)]\s+.+)$`)
	// Strip list prefix from a single numbered line; shared across matches.
	numberedListPrefixPattern = regexp.MustCompile(`^\d+[\.\)]\s*`)
)

func detectParallelTasks(input string) ([]any, bool) {
	matches := numberedTaskPattern.FindAllString(input, -1)
	if len(matches) >= 2 && len(matches) <= 4 {
		out := make([]any, 0, len(matches))
		for _, m := range matches {
			task := strings.TrimSpace(numberedTaskPattern.ReplaceAllString(m, "$1"))
			task = numberedListPrefixPattern.ReplaceAllString(task, "")
			if task == "" {
				continue
			}
			out = append(out, map[string]any{"prompt": task})
		}
		if len(out) >= 2 {
			return out, true
		}
	}

	lower := strings.ToLower(input)
	if strings.Contains(lower, " and ") {
		parts := strings.Split(input, " and ")
		if len(parts) >= 2 && len(parts) <= 4 {
			out := make([]any, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				out = append(out, map[string]any{"prompt": p})
			}
			if len(out) >= 2 {
				return out, true
			}
		}
	}
	return nil, false
}
func isCancelledByUser(rootCtx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if !errors.Is(err, context.Canceled) {
		return false
	}
	if rootCtx == nil {
		return false
	}
	return errors.Is(rootCtx.Err(), context.Canceled)
}

func permissionDeniedNote(m *permissions.Manager) string {
	if m.WasCancelled() {
		return "Cancelled."
	}
	return "Permission denied."
}
func shortModelName(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndex(name, "/"); i >= 0 && i+1 < len(name) {
		name = name[i+1:]
	}
	const maxLen = 32
	if len(name) > maxLen {
		name = name[:maxLen-1] + "…"
	}
	return name
}
func inferSingleToolCall(input string) (string, map[string]any, bool) {
	lower := strings.ToLower(input)
	if strings.Contains(lower, "using glob") || strings.Contains(lower, "use glob") {
		targetPath := "."
		if strings.Contains(lower, "./internal") || strings.Contains(lower, "internal") {
			targetPath = "./internal"
		}
		pattern := "*.go"
		if strings.Contains(lower, ".md") {
			pattern = "*.md"
		}
		return "Glob", map[string]any{
			"pattern": pattern,
			"path":    targetPath,
		}, true
	}
	return "", nil, false
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
