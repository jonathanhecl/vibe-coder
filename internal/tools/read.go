package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
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
					"file_path":  map[string]any{"type": "string"},
					"start_line": map[string]any{"type": "integer"},
					"end_line":   map[string]any{"type": "integer"},
					"offset":     map[string]any{"type": "integer"},
					"limit":      map[string]any{"type": "integer"},
					"max_bytes":  map[string]any{"type": "integer"},
				},
				"required": []string{"file_path"},
			},
		},
	}
}

func (t *ReadTool) Execute(ctx context.Context, params map[string]any) Result {
	path, ok := params["file_path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return errResult("file_path is required")
	}
	path = strings.TrimSpace(path)
	vr := validateExistingFileForRead(path)
	if vr.IsError() {
		return Result{Output: vr.UserError, HintsForModel: vr.AssistantHints, IsError: true}
	}

	file, err := os.Open(path)
	if err != nil {
		return errResult(fmt.Sprintf("read file: %v", err))
	}
	defer file.Close()

	startLine := asInt(params["start_line"], 0)
	if startLine <= 0 {
		offset := asInt(params["offset"], 0)
		if offset < 0 {
			offset = 0
		}
		startLine = offset + 1
	}
	endLine := asInt(params["end_line"], 0)
	limit := asInt(params["limit"], 0)
	if limit > 0 {
		limitEnd := startLine + limit - 1
		if endLine <= 0 || limitEnd < endLine {
			endLine = limitEnd
		}
	}
	maxBytes := asInt(params["max_bytes"], 0)

	// Default bufio.Scanner caps each line at 64KB. Godot .tscn, minified JSON,
	// and similar formats often use much longer single lines (tilemaps, embedded data).
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20) // up to 8 MiB per line
	var out strings.Builder
	lineNo := 1
	truncated := false
	for scanner.Scan() {
		if ctx.Err() != nil {
			return errResult(ctx.Err().Error())
		}
		if lineNo < startLine {
			lineNo++
			continue
		}
		if endLine > 0 && lineNo > endLine {
			truncated = true
			break
		}
		line := fmt.Sprintf("%6d|%s\n", lineNo, scanner.Text())
		if maxBytes > 0 && out.Len()+len(line) > maxBytes {
			remaining := maxBytes - out.Len()
			if remaining > 0 {
				out.WriteString(line[:remaining])
			}
			truncated = true
			break
		}
		out.WriteString(line)
		lineNo++
	}
	if err := scanner.Err(); err != nil {
		return errResult(fmt.Sprintf("scan file: %v", err))
	}
	if out.Len() == 0 && lineNo > 1 {
		return Result{Output: "No lines in requested range."}
	}
	if out.Len() == 0 {
		return Result{Output: "File is empty."}
	}
	output := strings.TrimRight(out.String(), "\n")
	if truncated {
		output += "\n[read truncated; adjust start_line/end_line/limit/max_bytes to continue]"
	}
	return Result{Output: output}
}
