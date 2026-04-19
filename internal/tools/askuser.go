package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
)

type AskUserQuestionTool struct{}

func NewAskUserQuestionTool() *AskUserQuestionTool { return &AskUserQuestionTool{} }

func (t *AskUserQuestionTool) Name() string { return "AskUserQuestion" }
func (t *AskUserQuestionTool) Description() string {
	return "Ask structured questions to the user and collect choices."
}
func (t *AskUserQuestionTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":     map[string]any{"type": "string"},
					"questions": map[string]any{"type": "array"},
				},
				"required": []string{"questions"},
			},
		},
	}
}

func (t *AskUserQuestionTool) Execute(_ context.Context, params map[string]any) Result {
	questions, ok := params["questions"].([]any)
	if !ok || len(questions) == 0 {
		return errResult("questions are required")
	}
	reader := bufio.NewReader(os.Stdin)
	answers := map[string]any{}
	for _, qAny := range questions {
		q, ok := qAny.(map[string]any)
		if !ok {
			continue
		}
		id, _ := q["id"].(string)
		prompt, _ := q["prompt"].(string)
		options, _ := q["options"].([]any)
		if id == "" || len(options) == 0 {
			continue
		}
		_, _ = os.Stdout.WriteString(prompt + "\n")
		for i, optAny := range options {
			if opt, ok := optAny.(map[string]any); ok {
				label, _ := opt["label"].(string)
				_, _ = os.Stdout.WriteString("- [" + intToString(i+1) + "] " + label + "\n")
			}
		}
		_, _ = os.Stdout.WriteString("Select option number: ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		chosen := "1"
		if line != "" {
			chosen = line
		}
		answers[id] = chosen
	}
	raw, _ := json.Marshal(answers)
	return Result{Output: string(raw)}
}
