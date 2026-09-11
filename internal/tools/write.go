package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type WriteTool struct{}

func NewWriteTool() *WriteTool { return &WriteTool{} }

func (t *WriteTool) Name() string { return "Write" }
func (t *WriteTool) Description() string {
	return "Write file contents atomically. Set append=true to add to the end of an existing file without overwriting it."
}
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
					"append": map[string]any{
						"type":        "boolean",
						"description": "Append to the end of the file instead of replacing its contents. Use this to add lines (e.g. hosts entries, JSONL records) without touching existing content.",
					},
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
	appendMode, _ := params["append"].(bool)
	path = strings.TrimSpace(path)
	vr := validateWriteTargetPath(path)
	if vr.IsError() {
		return Result{Output: vr.UserError, HintsForModel: vr.AssistantHints, IsError: true}
	}

	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Result{Output: agentPathPreamble(fmt.Sprintf("create parent dir: %v", err)), HintsForModel: assistantPathHints(parent, "mkdir parent", err), IsError: true}
	}
	if appendMode {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return errResult(fmt.Sprintf("open file for append: %v", err))
		}
		if _, err := file.WriteString(contents); err != nil {
			_ = file.Close()
			return errResult(fmt.Sprintf("append to file: %v", err))
		}
		if err := file.Close(); err != nil {
			return errResult(fmt.Sprintf("close appended file: %v", err))
		}
		return Result{Output: "Append successful."}
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
