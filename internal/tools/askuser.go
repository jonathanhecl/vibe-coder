package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

const askUserQuestionExample = `{"questions":[{"id":"which","prompt":"Which project do you mean?","options":[{"id":"here","label":"This workspace"},{"id":"other","label":"Another folder"}]}]}`

type AskUserQuestionTool struct{}

func NewAskUserQuestionTool() *AskUserQuestionTool { return &AskUserQuestionTool{} }

func (t *AskUserQuestionTool) Name() string { return "AskUserQuestion" }
func (t *AskUserQuestionTool) Description() string {
	return `Ask the user to pick from labeled options when something is ambiguous. ` +
		`The JSON body must use "questions" as an array of OBJECTS (each with "prompt" and "options"), not an array of plain strings. ` +
		`Each option may be a string or {"label":"...","id":"..."}. ` +
		`Example: ` + askUserQuestionExample
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
					"title": map[string]any{
						"type":        "string",
						"description": "Optional heading printed before the questions.",
					},
					"questions": map[string]any{
						"type": "array",
						"description": `Required. Each item is an object: {"id":"q1","prompt":"...","options":["A","B"]} or options as [{"label":"A","id":"a"},...].`,
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id":      map[string]any{"type": "string"},
								"prompt":  map[string]any{"type": "string"},
								"options": map[string]any{"type": "array"},
							},
							"required": []string{"prompt", "options"},
						},
					},
				},
				"required": []string{"questions"},
			},
		},
	}
}

func (t *AskUserQuestionTool) Execute(_ context.Context, params map[string]any) Result {
	questions, errMsg := parseAskQuestions(params)
	if errMsg != "" {
		return errResult(errMsg)
	}
	title, _ := params["title"].(string)
	title = strings.TrimSpace(title)

	reader := bufio.NewReader(os.Stdin)
	answers := map[string]any{}

	if title != "" {
		_, _ = os.Stdout.WriteString(title + "\n\n")
	}

	for _, q := range questions {
		_, _ = os.Stdout.WriteString(q.prompt + "\n")
		for i, opt := range q.options {
			_, _ = os.Stdout.WriteString("- [" + intToString(i+1) + "] " + opt.label + "\n")
		}
		_, _ = os.Stdout.WriteString("Select option number: ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		chosenIdx := 1
		if line != "" {
			if n, err := strconv.Atoi(line); err == nil && n >= 1 {
				chosenIdx = n
			}
		}
		if chosenIdx < 1 || chosenIdx > len(q.options) {
			chosenIdx = 1
		}
		sel := q.options[chosenIdx-1]
		if strings.TrimSpace(sel.id) != "" {
			answers[q.id] = sel.id
		} else {
			answers[q.id] = intToString(chosenIdx)
		}
	}
	raw, _ := json.Marshal(answers)
	return Result{Output: string(raw)}
}
