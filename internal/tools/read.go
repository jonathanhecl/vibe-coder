package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/safety"
)

type ReadTool struct{}

func NewReadTool() *ReadTool { return &ReadTool{} }

func (t *ReadTool) Name() string { return "Read" }
func (t *ReadTool) Description() string {
	return "Read a text file with line numbers."
}
func (t *ReadTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string"},
				},
				"required": []string{"file_path"},
			},
		},
	}
}

func (t *ReadTool) Execute(_ context.Context, params map[string]any) Result {
	path, ok := params["file_path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return errResult("file_path is required")
	}
	if !filepath.IsAbs(path) {
		return errResult("file_path must be absolute")
	}
	if safety.IsProtectedPath(path) {
		return errResult("path is protected")
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errResult("refusing to read symlink")
	}

	file, err := os.Open(path)
	if err != nil {
		return errResult(fmt.Sprintf("read file: %v", err))
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var out strings.Builder
	lineNo := 1
	for scanner.Scan() {
		out.WriteString(fmt.Sprintf("%6d|%s\n", lineNo, scanner.Text()))
		lineNo++
	}
	if err := scanner.Err(); err != nil {
		return errResult(fmt.Sprintf("scan file: %v", err))
	}
	if out.Len() == 0 {
		return Result{Output: "File is empty."}
	}
	return Result{Output: strings.TrimRight(out.String(), "\n")}
}
