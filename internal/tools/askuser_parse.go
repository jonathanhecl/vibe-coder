package tools

import (
	"fmt"
	"strconv"
	"strings"
)

type askOption struct {
	id    string
	label string
}

type askQuestion struct {
	id      string
	prompt  string
	options []askOption
}

// parseAskQuestions builds a normalized question list from tool params.
// It returns a non-empty error message if the payload is unusable (so the model can fix the JSON).
func parseAskQuestions(params map[string]any) ([]askQuestion, string) {
	raw, ok := params["questions"]
	if !ok {
		return nil, `missing "questions" — use a non-empty array of objects, not strings. Example: {"questions":[{"id":"q1","prompt":"Pick one","options":["A","B"]}]}`
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, `"questions" must be a non-empty JSON array of objects. Do not use an array of plain question strings.`
	}
	out := make([]askQuestion, 0, len(list))
	for i, qAny := range list {
		q, err := normalizeOneQuestion(qAny, i)
		if err != "" {
			return nil, err
		}
		out = append(out, q)
	}
	return out, ""
}

func normalizeOneQuestion(qAny any, index int) (askQuestion, string) {
	m, ok := qAny.(map[string]any)
	if !ok {
		return askQuestion{}, fmt.Sprintf(
			`questions[%d] must be a JSON object with "id", "prompt", and "options" — got %T. Do not pass an array of question strings; wrap each in {"id":"...","prompt":"...","options":[...]}.`,
			index, qAny,
		)
	}
	id, _ := m["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		id = "q" + strconv.Itoa(index+1)
	}
	prompt, _ := m["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "Choose an option:"
	}
	optsRaw, ok := m["options"].([]any)
	if !ok || len(optsRaw) == 0 {
		return askQuestion{}, fmt.Sprintf(`question %q has no "options" array (need at least one choice).`, id)
	}
	opts, err := normalizeOptions(optsRaw)
	if err != "" {
		return askQuestion{}, fmt.Sprintf("question %q: %s", id, err)
	}
	return askQuestion{id: id, prompt: prompt, options: opts}, ""
}

func normalizeOptions(optsRaw []any) ([]askOption, string) {
	opts := make([]askOption, 0, len(optsRaw))
	for j, oAny := range optsRaw {
		switch v := oAny.(type) {
		case string:
			s := strings.TrimSpace(v)
			if s == "" {
				return nil, fmt.Sprintf("options[%d] is an empty string", j)
			}
			opts = append(opts, askOption{id: strconv.Itoa(j + 1), label: s})
		case map[string]any:
			label, _ := v["label"].(string)
			label = strings.TrimSpace(label)
			if label == "" {
				return nil, fmt.Sprintf("options[%d] needs a non-empty \"label\"", j)
			}
			oid, _ := v["id"].(string)
			oid = strings.TrimSpace(oid)
			if oid == "" {
				oid = strconv.Itoa(j + 1)
			}
			opts = append(opts, askOption{id: oid, label: label})
		default:
			return nil, fmt.Sprintf("options[%d] must be a string or {\"label\":\"...\"}", j)
		}
	}
	if len(opts) == 0 {
		return nil, "need at least one option"
	}
	return opts, ""
}
