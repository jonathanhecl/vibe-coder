package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

const askUserQuestionExample = `{"questions":[{"id":"which","prompt":"Which project do you mean?","options":[{"id":"here","label":"This workspace"},{"id":"other","label":"Another folder"}]}]}`

type AskUserQuestionTool struct {
	in  io.Reader
	out io.Writer
}

func NewAskUserQuestionTool() *AskUserQuestionTool { return &AskUserQuestionTool{} }

func NewAskUserQuestionToolWithIO(in io.Reader, out io.Writer) *AskUserQuestionTool {
	return &AskUserQuestionTool{in: in, out: out}
}

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
						"type":        "array",
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

	in := t.in
	if in == nil {
		in = os.Stdin
	}
	out := t.out
	if out == nil {
		out = os.Stdout
	}
	st := tui.NewStyle(out)
	reader := bufio.NewReader(in)
	answers := map[string]any{}

	if title != "" {
		fmt.Fprintf(out, "\n  %s %s\n", st.BoldCyan("?"), st.Bold(title))
	}

	for _, q := range questions {
		if title != "" {
			fmt.Fprintf(out, "    %s\n\n", st.BrightWhite(q.prompt))
		} else {
			fmt.Fprintf(out, "\n  %s %s\n\n", st.BoldCyan("?"), st.Bold(q.prompt))
		}

		for i, opt := range q.options {
			num := strconv.Itoa(i + 1)
			fmt.Fprintf(out, "    %s %s\n",
				st.BoldCyan("["+num+"]"),
				st.BrightWhite(opt.label),
			)
		}

		promptText := fmt.Sprintf("Select option (1-%d) [default: 1]: ", len(q.options))
		fmt.Fprintf(out, "\n  %s %s", st.BoldCyan("▸"), st.Dim(promptText))

		chosenIdx := 1
		for {
			line, err := reader.ReadString('\n')
			if err != nil && line == "" {
				chosenIdx = 1
				break
			}
			line = strings.TrimSpace(line)
			if line == "" {
				chosenIdx = 1
				break
			}

			// 1. Try matching by 1-based index
			if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(q.options) {
				chosenIdx = n
				break
			}

			// 2. Try matching by option ID (case-insensitive)
			matched := -1
			for i, opt := range q.options {
				if opt.id != "" && strings.EqualFold(opt.id, line) {
					matched = i + 1
					break
				}
			}
			if matched != -1 {
				chosenIdx = matched
				break
			}

			// 3. Try matching by option label (exact or prefix, case-insensitive)
			lower := strings.ToLower(line)
			for i, opt := range q.options {
				optLower := strings.ToLower(opt.label)
				if optLower == lower || strings.HasPrefix(optLower, lower) {
					matched = i + 1
					break
				}
			}
			if matched != -1 {
				chosenIdx = matched
				break
			}

			// If input couldn't be parsed and at EOF, default to 1
			if err != nil {
				chosenIdx = 1
				break
			}

			// Re-prompt user
			fmt.Fprintf(out, "  %s\n  %s %s",
				st.DimYellow(fmt.Sprintf("Invalid option %q. Please enter a number (1-%d):", line, len(q.options))),
				st.BoldCyan("▸"),
				st.Dim("Select option: "),
			)
		}

		if chosenIdx < 1 || chosenIdx > len(q.options) {
			chosenIdx = 1
		}
		sel := q.options[chosenIdx-1]
		if strings.TrimSpace(sel.id) != "" {
			answers[q.id] = sel.id
		} else {
			answers[q.id] = strconv.Itoa(chosenIdx)
		}

		// Immediate visual feedback of the selected option
		fmt.Fprintf(out, "  %s %s %s %s\n\n",
			st.BoldBrightGreen("✓"),
			st.Dim("Selected:"),
			st.BoldBrightGreen("["+strconv.Itoa(chosenIdx)+"]"),
			st.BrightWhite(sel.label),
		)
	}

	raw, _ := json.Marshal(answers)
	return Result{Output: string(raw)}
}
