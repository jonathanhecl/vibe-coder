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

// AskOption is one labeled choice offered to the user.
type AskOption struct {
	ID    string
	Label string
}

// AskRequest is a single question handed to an Asker. ErrNote carries the
// reason the previous answer was rejected so an interactive UI can show it
// above the prompt instead of silently starting over.
type AskRequest struct {
	Title   string
	Prompt  string
	Options []AskOption
	ErrNote string
}

// Asker renders a question and returns the raw line the user typed, or "" for
// the default option. Answer matching and re-asking stay in the tool so every
// front end shares one implementation of the accepted formats.
type Asker interface {
	Ask(req AskRequest) (string, error)
}

type AskUserQuestionTool struct {
	in    io.Reader
	out   io.Writer
	asker Asker
}

func NewAskUserQuestionTool() *AskUserQuestionTool { return &AskUserQuestionTool{} }

func NewAskUserQuestionToolWithIO(in io.Reader, out io.Writer) *AskUserQuestionTool {
	return &AskUserQuestionTool{in: in, out: out}
}

// SetAsker routes prompts through a UI that already owns stdin. Without one the
// tool reads stdin itself, which is only safe when no other reader (the TUI
// line editor or the ESC monitor) is active on the same terminal.
func (t *AskUserQuestionTool) SetAsker(a Asker) { t.asker = a }

func (t *AskUserQuestionTool) Name() string { return "AskUserQuestion" }
func (t *AskUserQuestionTool) Description() string {
	return `Ask the user to pick from labeled options when something is ambiguous. ` +
		`For a single question, provide "prompt" and "options" (strings or {"label":"...","id":"..."}). ` +
		`For multiple questions, provide "questions" as an array of objects (each with "prompt" and "options"). ` +
		`Example single question: {"prompt":"Which framework?","options":["React","Vue"]} ` +
		`Example multiple questions: ` + askUserQuestionExample
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
					"prompt": map[string]any{
						"type":        "string",
						"description": "The question prompt when asking a single question.",
					},
					"options": map[string]any{
						"type":        "array",
						"description": `List of options when using "prompt". Strings or objects with {"label":"...","id":"..."}.`,
					},
					"questions": map[string]any{
						"type":        "array",
						"description": `List of questions for multiple questions. Each item is an object: {"id":"q1","prompt":"...","options":["A","B"]}.`,
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

	out := t.out
	if out == nil {
		out = os.Stdout
	}
	st := tui.NewStyle(out)
	answers := map[string]any{}

	// With an asker the interactive UI owns stdin and echoes the answer. Only
	// the headless path reads stdin here.
	var reader *bufio.Reader
	if t.asker == nil {
		in := t.in
		if in == nil {
			in = os.Stdin
		}
		reader = bufio.NewReader(in)
	}

	for qIndex, q := range questions {
		cardTitle := ""
		if qIndex == 0 {
			cardTitle = title
		}
		chosenIdx, err := t.ask(st, out, reader, cardTitle, q)
		if err != nil {
			return errResult(err.Error())
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

// ask resolves one question through the interactive asker when one is wired up,
// and otherwise by printing the card and reading stdin.
func (t *AskUserQuestionTool) ask(st tui.Style, out io.Writer, reader *bufio.Reader, title string, q askQuestion) (int, error) {
	if t.asker != nil {
		return t.askWithUI(title, q)
	}
	return matchOnStdin(st, out, reader, title, q), nil
}

// askWithUI lets the UI own stdin: it renders the card and returns the line it
// read with echo enabled. Matching stays here, so a rejected answer is
// re-asked with the reason attached instead of restarting the card.
func (t *AskUserQuestionTool) askWithUI(title string, q askQuestion) (int, error) {
	opts := make([]AskOption, len(q.options))
	for i, opt := range q.options {
		opts[i] = AskOption{ID: opt.id, Label: opt.label}
	}
	note := ""
	for {
		line, err := t.asker.Ask(AskRequest{Title: title, Prompt: q.prompt, Options: opts, ErrNote: note})
		if err != nil {
			return 0, fmt.Errorf("question cancelled: %w", err)
		}
		if idx, ok := matchAskAnswer(line, q.options); ok {
			return idx, nil
		}
		note = fmt.Sprintf("Invalid option %q. Please enter a number (1-%d):", strings.TrimSpace(line), len(q.options))
	}
}

// matchOnStdin prints the card and reads answers from the given reader.
func matchOnStdin(st tui.Style, out io.Writer, reader *bufio.Reader, title string, q askQuestion) int {
	printAskCard(st, out, title, q)
	for {
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return 1
		}
		if idx, ok := matchAskAnswer(line, q.options); ok {
			return idx
		}
		if err != nil {
			return 1
		}
		fmt.Fprintf(out, "  %s\n  %s %s",
			st.DimYellow(fmt.Sprintf("Invalid option %q. Please enter a number (1-%d):", strings.TrimSpace(line), len(q.options))),
			st.BoldCyan("▸"),
			st.Dim("Select option: "),
		)
	}
}

func printAskCard(st tui.Style, out io.Writer, title string, q askQuestion) {
	if title != "" {
		fmt.Fprintf(out, "\n  %s %s\n    %s\n\n", st.BoldCyan("?"), st.Bold(title), st.BrightWhite(q.prompt))
	} else {
		fmt.Fprintf(out, "\n  %s %s\n\n", st.BoldCyan("?"), st.Bold(q.prompt))
	}
	for i, opt := range q.options {
		fmt.Fprintf(out, "    %s %s\n",
			st.BoldCyan("["+strconv.Itoa(i+1)+"]"),
			st.BrightWhite(opt.label),
		)
	}
	fmt.Fprintf(out, "\n  %s %s", st.BoldCyan("▸"), st.Dim(fmt.Sprintf("Select option (1-%d) [default: 1]: ", len(q.options))))
}

// matchAskAnswer maps a typed line to a 1-based option index. An index, an
// option id, or a label prefix (case-insensitive) all select an option; an
// empty line takes the default first option.
func matchAskAnswer(line string, options []askOption) (int, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return 1, true
	}
	if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(options) {
		return n, true
	}
	for i, opt := range options {
		if opt.id != "" && strings.EqualFold(opt.id, line) {
			return i + 1, true
		}
	}
	lower := strings.ToLower(line)
	for i, opt := range options {
		optLower := strings.ToLower(opt.label)
		if optLower == lower || strings.HasPrefix(optLower, lower) {
			return i + 1, true
		}
	}
	return 0, false
}
