package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type NotebookEditTool struct{}

func NewNotebookEditTool() *NotebookEditTool { return &NotebookEditTool{} }

func (t *NotebookEditTool) Name() string { return "NotebookEdit" }
func (t *NotebookEditTool) Description() string {
	return "Edit a Jupyter notebook cell."
}
func (t *NotebookEditTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"notebook_path": map[string]any{"type": "string"},
					"cell_index":    map[string]any{"type": "integer"},
					"new_string":    map[string]any{"type": "string"},
					"old_string":    map[string]any{"type": "string"},
					"is_new_cell":   map[string]any{"type": "boolean"},
					"cell_language": map[string]any{"type": "string"},
				},
				"required": []string{"notebook_path", "cell_index", "new_string"},
			},
		},
	}
}

type notebook struct {
	Cells []notebookCell `json:"cells"`
}

type notebookCell struct {
	CellType string   `json:"cell_type"`
	Source   []string `json:"source"`
}

func (t *NotebookEditTool) Execute(_ context.Context, params map[string]any) Result {
	path, ok := params["notebook_path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return errResult("notebook_path is required")
	}
	path = strings.TrimSpace(path)
	if msg := validateExistingFileForRead(path); msg != "" {
		if msg == "refusing to read symlink" {
			return errResult("refusing symlink notebook")
		}
		return errResult(msg)
	}

	cellIndex := asInt(params["cell_index"], -1)
	if cellIndex < 0 {
		return errResult("cell_index must be >= 0")
	}
	newString, _ := params["new_string"].(string)
	oldString, _ := params["old_string"].(string)
	isNewCell := asBool(params["is_new_cell"])
	cellLang, _ := params["cell_language"].(string)
	if cellLang == "" {
		cellLang = "python"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return errResult(agentPathPreamble(fmt.Sprintf("read notebook: %v", err)) + assistantPathHints(path, "read notebook", err))
	}
	var nb notebook
	if err := json.Unmarshal(data, &nb); err != nil {
		return errResult(fmt.Sprintf("decode notebook: %v", err))
	}

	if isNewCell {
		if cellIndex > len(nb.Cells) {
			cellIndex = len(nb.Cells)
		}
		cell := notebookCell{
			CellType: normalizeCellLanguage(cellLang),
			Source:   []string{newString},
		}
		nb.Cells = append(nb.Cells[:cellIndex], append([]notebookCell{cell}, nb.Cells[cellIndex:]...)...)
	} else {
		if cellIndex >= len(nb.Cells) {
			return errResult("cell_index out of range")
		}
		current := strings.Join(nb.Cells[cellIndex].Source, "")
		if oldString != "" && !strings.Contains(current, oldString) {
			return errResult("old_string not found in target cell")
		}
		updated := newString
		if oldString != "" {
			updated = strings.Replace(current, oldString, newString, 1)
		}
		nb.Cells[cellIndex].Source = []string{updated}
	}

	raw, err := json.MarshalIndent(nb, "", "  ")
	if err != nil {
		return errResult(fmt.Sprintf("encode notebook: %v", err))
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return errResult(agentPathPreamble(fmt.Sprintf("write notebook: %v", err)) + assistantPathHints(path, "write notebook", err))
	}
	return Result{Output: "Notebook updated."}
}

func normalizeCellLanguage(lang string) string {
	l := strings.ToLower(lang)
	switch l {
	case "markdown", "raw":
		return l
	default:
		return "code"
	}
}
