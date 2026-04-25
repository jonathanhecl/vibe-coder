package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

type EditTool struct{}

func NewEditTool() *EditTool { return &EditTool{} }

func unifiedDiff(a, b string) string {
	al := strings.Split(a, "\n")
	bl := strings.Split(b, "\n")
	i := 0
	for i < len(al) && i < len(bl) && al[i] == bl[i] {
		i++
	}
	j, k := len(al)-1, len(bl)-1
	for j >= i && k >= i && al[j] == bl[k] {
		j--
		k--
	}
	if i > j && i > k {
		return ""
	}
	const c = 3
	lo := max(0, i-c)
	ro := min(len(al), j+1+c)
	rb := min(len(bl), k+1+c)
	var sb strings.Builder
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", lo+1, ro-lo, lo+1, rb-lo)
	o, n := lo, lo
	for o < ro || n < rb {
		if o < ro && n < rb && al[o] == bl[n] && (o < i || o > j) {
			fmt.Fprintf(&sb, " %s\n", al[o])
			o++
			n++
			continue
		}
		if o <= j && o < ro {
			fmt.Fprintf(&sb, "-%s\n", al[o])
			o++
		} else if n <= k && n < rb {
			fmt.Fprintf(&sb, "+%s\n", bl[n])
			n++
		} else if o < ro {
			fmt.Fprintf(&sb, " %s\n", al[o])
			o++
			n++
		} else {
			fmt.Fprintf(&sb, "+%s\n", bl[n])
			n++
		}
	}
	return sb.String()
}

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
	vr := validateExistingFileForRead(path)
	if vr.IsError() {
		return Result{Output: vr.UserError, HintsForModel: vr.AssistantHints, IsError: true}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{Output: agentPathPreamble(fmt.Sprintf("read file: %v", err)), HintsForModel: assistantPathHints(path, "read", err), IsError: true}
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
	res := NewWriteTool().Execute(ctx, map[string]any{
		"file_path": path,
		"contents":  updated,
	})
	res.Diff = unifiedDiff(content, updated)
	return res
}
