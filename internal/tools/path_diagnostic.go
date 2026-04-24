package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/safety"
)

// validationResult holds a user-facing error summary and optional extra hints
// for the model (assistant) that should not clutter the user-visible output.
type validationResult struct {
	UserError      string
	AssistantHints string
}

func (r validationResult) IsError() bool { return r.UserError != "" }

// validateExistingFileForRead checks path before opening an existing file (Read,
// Edit, NotebookEdit). Returns a non-empty validationResult if the path is bad.
func validateExistingFileForRead(path string) validationResult {
	path = strings.TrimSpace(path)
	if path == "" {
		return validationResult{UserError: "PATH ERROR: path is empty.", AssistantHints: assistantPathHints(path, "read", nil)}
	}
	if !filepath.IsAbs(path) {
		return validationResult{UserError: "PATH ERROR: path must be an absolute filesystem path.", AssistantHints: assistantPathHints(path, "read", nil)}
	}
	if safety.IsProtectedPath(path) {
		return validationResult{UserError: "path is protected"}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return validationResult{UserError: fmt.Sprintf("PATH ERROR: cannot access path: %v.", err), AssistantHints: assistantPathHints(path, "read", err)}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return validationResult{UserError: "refusing to read symlink"}
	}
	if info.IsDir() {
		return validationResult{UserError: "PATH ERROR: path is a directory; expected a file.", AssistantHints: assistantPathHints(path, "read", nil)}
	}
	return validationResult{}
}

// validateWriteTargetPath checks path before Write (file may not exist yet).
func validateWriteTargetPath(path string) validationResult {
	path = strings.TrimSpace(path)
	if path == "" {
		return validationResult{UserError: "PATH ERROR: path is empty.", AssistantHints: assistantPathHints(path, "write", nil)}
	}
	if !filepath.IsAbs(path) {
		return validationResult{UserError: "PATH ERROR: path must be an absolute filesystem path.", AssistantHints: assistantPathHints(path, "write", nil)}
	}
	if safety.IsProtectedPath(path) {
		return validationResult{UserError: "path is protected"}
	}
	if _, err := filepath.Abs(path); err != nil {
		return validationResult{UserError: fmt.Sprintf("PATH ERROR: invalid path: %v.", err), AssistantHints: assistantPathHints(path, "write", err)}
	}

	info, err := os.Lstat(path)
	if err == nil {
		if info.IsDir() {
			return validationResult{UserError: "PATH ERROR: file_path is a directory; Write expects a regular file path.", AssistantHints: assistantPathHints(path, "write", nil)}
		}
		return validationResult{}
	}
	if !errors.Is(err, os.ErrNotExist) {
		return validationResult{UserError: fmt.Sprintf("PATH ERROR: cannot access path: %v.", err), AssistantHints: assistantPathHints(path, "write", err)}
	}

	parent := filepath.Dir(path)
	pst, perr := os.Lstat(parent)
	if perr != nil {
		if errors.Is(perr, os.ErrNotExist) {
			return validationResult{}
		}
		return validationResult{UserError: fmt.Sprintf("PATH ERROR: cannot access parent directory %q: %v.", parent, perr), AssistantHints: assistantPathHints(parent, "write parent", perr)}
	}
	if !pst.IsDir() {
		return validationResult{UserError: fmt.Sprintf("PATH ERROR: parent %q is not a directory.", parent), AssistantHints: assistantPathHints(parent, "write parent", nil)}
	}
	return validationResult{}
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
