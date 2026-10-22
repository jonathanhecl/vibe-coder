package tools

import (
	"fmt"
	"strconv"
	"strings"
)

type askOption struct {
	id          string
	label       string
	description string
	// recommended marks the option the model judges best. It is promoted to
	// position 1 because position 1 is the default taken by ESC and the
	// timeout: an instruction alone was not enough for every model.
	recommended bool
	// autoID records an id derived from the position, so promotion can
	// renumber it and keep "id" meaningful for the model.
	autoID bool
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
		// Tolerant fallback: check if model passed top-level prompt (or question) and options
		if q, err := parseTopLevelPromptQuestion(params); err == "" && len(q) > 0 {
			return q, ""
		}
		return nil, `missing "questions" (or "prompt" and "options") — use {"prompt":"...","options":["A","B"]} or {"questions":[{"id":"q1","prompt":"...","options":["A","B"]}]}`
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		if q, err := parseTopLevelPromptQuestion(params); err == "" && len(q) > 0 {
			return q, ""
		}
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

func parseTopLevelPromptQuestion(params map[string]any) ([]askQuestion, string) {
	prompt, _ := params["prompt"].(string)
	if prompt == "" {
		prompt, _ = params["question"].(string)
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, ""
	}
	optsRaw, ok := params["options"].([]any)
	if !ok || len(optsRaw) == 0 {
		return nil, ""
	}
	opts, err := normalizeOptions(optsRaw)
	if err != "" {
		return nil, err
	}
	id, _ := params["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		id = "q1"
	}
	return []askQuestion{{id: id, prompt: prompt, options: opts}}, ""
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
	if prompt == "" {
		prompt, _ = m["question"].(string)
	}
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
			opts = append(opts, askOption{id: strconv.Itoa(j + 1), label: s, autoID: true})
		case map[string]any:
			label, _ := v["label"].(string)
			label = strings.TrimSpace(label)
			if label == "" {
				return nil, fmt.Sprintf("options[%d] needs a non-empty \"label\"", j)
			}
			oid, _ := v["id"].(string)
			oid = strings.TrimSpace(oid)
			autoID := oid == ""
			if autoID {
				oid = strconv.Itoa(j + 1)
			}
			desc, _ := v["description"].(string)
			if strings.TrimSpace(desc) == "" {
				desc, _ = v["desc"].(string)
			}
			rec := isTrue(v["recommended"]) || isTrue(v["best"])
			opts = append(opts, askOption{
				id:          oid,
				label:       label,
				description: strings.TrimSpace(desc),
				recommended: rec,
				autoID:      autoID,
			})
		default:
			return nil, fmt.Sprintf("options[%d] must be a string or {\"label\":\"...\",\"description\":\"...\"}", j)
		}
	}
	if len(opts) == 0 {
		return nil, "need at least one option"
	}
	return promoteRecommended(opts), ""
}

// isTrue accepts the shapes a model may emit for a boolean-ish field.
func isTrue(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		s := strings.ToLower(strings.TrimSpace(b))
		return s == "true" || s == "yes" || s == "1"
	case float64:
		return b != 0
	}
	return false
}

// promoteRecommended moves the option flagged as recommended to position 1,
// because position 1 is what ESC, an empty answer and the timeout select. The
// other options keep their relative order and positional ids are renumbered so
// the answer the model receives still points at the option it sees.
func promoteRecommended(opts []askOption) []askOption {
	idx := -1
	for i, o := range opts {
		if o.recommended {
			idx = i
			break
		}
	}
	if idx > 0 {
		rec := opts[idx]
		copied := make([]askOption, 0, len(opts))
		copied = append(copied, rec)
		copied = append(copied, opts[:idx]...)
		copied = append(copied, opts[idx+1:]...)
		opts = copied
	}
	for i := range opts {
		opts[i].recommended = false
		if opts[i].autoID {
			opts[i].id = strconv.Itoa(i + 1)
		}
	}
	return opts
}
