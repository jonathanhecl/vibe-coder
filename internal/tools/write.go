package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/safety"
)

type WriteTool struct{}

func NewWriteTool() *WriteTool { return &WriteTool{} }

func (t *WriteTool) Name() string        { return "Write" }
func (t *WriteTool) Description() string { return "Write file contents atomically." }
func (t *WriteTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string"},
					"contents":  map[string]any{"type": "string"},
				},
				"required": []string{"file_path", "contents"},
			},
		},
	}
}

func (t *WriteTool) Execute(_ context.Context, params map[string]any) Result {
	path, ok := params["file_path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return errResult("file_path is required")
	}
	contents, ok := params["contents"].(string)
	if !ok {
		return errResult("contents must be a string")
	}
	if !filepath.IsAbs(path) {
		return errResult("file_path must be absolute")
	}
	if safety.IsProtectedPath(path) {
		return errResult("path is protected")
	}

	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return errResult(fmt.Sprintf("create parent dir: %v", err))
	}
	tmp, err := os.CreateTemp(parent, "*.write.tmp")
	if err != nil {
		return errResult(fmt.Sprintf("create temp file: %v", err))
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(contents); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return errResult(fmt.Sprintf("write temp file: %v", err))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return errResult(fmt.Sprintf("close temp file: %v", err))
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return errResult(fmt.Sprintf("chmod temp file: %v", err))
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return errResult(fmt.Sprintf("rename temp file: %v", err))
	}
	return Result{Output: "Write successful."}
}
