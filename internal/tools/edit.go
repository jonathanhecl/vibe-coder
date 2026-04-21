package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

type EditTool struct{}

func NewEditTool() *EditTool { return &EditTool{} }

func (t *EditTool) Name() string { return "Edit" }
func (t *EditTool) Description() string {
	return "Edit file content by replacing exact text."
}
func (t *EditTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path":   map[string]any{"type": "string"},
					"old_string":  map[string]any{"type": "string"},
					"new_string":  map[string]any{"type": "string"},
					"replace_all": map[string]any{"type": "boolean"},
				},
				"required": []string{"file_path", "old_string", "new_string"},
			},
		},
	}
}

func (t *EditTool) Execute(ctx context.Context, params map[string]any) Result {
	path, ok := params["file_path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return errResult("file_path is required")
	}
	oldString, ok := params["old_string"].(string)
	if !ok || oldString == "" {
		return errResult("old_string is required")
	}
	newString, ok := params["new_string"].(string)
	if !ok {
		return errResult("new_string must be string")
	}
	replaceAll, _ := params["replace_all"].(bool)

	path = strings.TrimSpace(path)
	if msg := validateExistingFileForRead(path); msg != "" {
		return errResult(msg)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errResult(agentPathPreamble(fmt.Sprintf("read file: %v", err)) + assistantPathHints(path, "read", err))
	}
	content := string(data)
	count := strings.Count(content, oldString)
	if count == 0 {
		return errResult("old_string not found")
	}
	if count > 1 && !replaceAll {
		return errResult("old_string matched multiple times, set replace_all=true")
	}
	limit := 1
	if replaceAll {
		limit = -1
	}
	updated := strings.Replace(content, oldString, newString, limit)
	return NewWriteTool().Execute(ctx, map[string]any{
		"file_path": path,
		"contents":  updated,
	})
}
