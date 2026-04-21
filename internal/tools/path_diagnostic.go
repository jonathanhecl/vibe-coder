package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/safety"
)

// validateExistingFileForRead checks path before opening an existing file (Read,
// Edit, NotebookEdit). Returns a non-empty agent-oriented message to return as
// tool error, or "" if the path looks OK to open (caller may still fail I/O).
func validateExistingFileForRead(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return agentPathPreamble("path is empty") + assistantPathHints(path, "read", nil)
	}
	if !filepath.IsAbs(path) {
		return agentPathPreamble("path must be an absolute filesystem path") + assistantPathHints(path, "read", nil)
	}
	if safety.IsProtectedPath(path) {
		return "path is protected"
	}
	info, err := os.Lstat(path)
	if err != nil {
		return agentPathPreamble(fmt.Sprintf("cannot access path: %v", err)) + assistantPathHints(path, "read", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "refusing to read symlink"
	}
	if info.IsDir() {
		return agentPathPreamble("path is a directory; expected a file") + assistantPathHints(path, "read", nil)
	}
	return ""
}

// validateWriteTargetPath checks path before Write (file may not exist yet).
func validateWriteTargetPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return agentPathPreamble("path is empty") + assistantPathHints(path, "write", nil)
	}
	if !filepath.IsAbs(path) {
		return agentPathPreamble("path must be an absolute filesystem path") + assistantPathHints(path, "write", nil)
	}
	if safety.IsProtectedPath(path) {
		return "path is protected"
	}
	if _, err := filepath.Abs(path); err != nil {
		return agentPathPreamble(fmt.Sprintf("invalid path: %v", err)) + assistantPathHints(path, "write", err)
	}

	info, err := os.Lstat(path)
	if err == nil {
		if info.IsDir() {
			return agentPathPreamble("file_path is a directory; Write expects a regular file path") + assistantPathHints(path, "write", nil)
		}
		return ""
	}
	if !errors.Is(err, os.ErrNotExist) {
		return agentPathPreamble(fmt.Sprintf("cannot access path: %v", err)) + assistantPathHints(path, "write", err)
	}

	parent := filepath.Dir(path)
	pst, perr := os.Lstat(parent)
	if perr != nil {
		if errors.Is(perr, os.ErrNotExist) {
			return ""
		}
		return agentPathPreamble(fmt.Sprintf("cannot access parent directory %q: %v", parent, perr)) + assistantPathHints(parent, "write parent", perr)
	}
	if !pst.IsDir() {
		return agentPathPreamble(fmt.Sprintf("parent %q is not a directory", parent)) + assistantPathHints(parent, "write parent", nil)
	}
	return ""
}

func agentPathPreamble(summary string) string {
	return "PATH ERROR: " + summary + ".\n\n"
}

// assistantPathHints adds stable guidance for the model when paths are wrong,
// without assuming a specific stack (Godot, Unity, etc.).
func assistantPathHints(path string, ctx string, osErr error) string {
	var b strings.Builder
	b.WriteString("For the assistant (fix the path and retry):\n")
	b.WriteString("- Use a real absolute path that exists on disk under the workspace (from Glob, Grep, or earlier tool output).\n")
	if strings.Contains(path, "://") {
		b.WriteString("- The path contains \"://\" (URL or engine resource scheme). Those are not OS paths here; resolve to the actual file path on disk.\n")
	}
	if osErr != nil && errors.Is(osErr, os.ErrNotExist) {
		b.WriteString("- Nothing exists at this location; re-check spelling, directory, and drive letter.\n")
	}
	if ctx != "" {
		b.WriteString(fmt.Sprintf("- Context: %s.\n", ctx))
	}
	b.WriteString("- If unsure, run Glob with a pattern such as **/filename before Read or Edit.\n")
	return b.String()
}
