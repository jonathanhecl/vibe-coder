package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/decision"
)

// DecideTool exposes the decision model as a tool for the agent and sidecar,
// allowing the model to request a fast, deterministic second opinion, classification,
// or validation decision over a list of options.
type DecideTool struct {
	decider decision.Decider
}

// NewDecideTool constructs a new DecideTool.
func NewDecideTool(decider decision.Decider) *DecideTool {
	return &DecideTool{decider: decider}
}

func (t *DecideTool) Name() string { return "Decide" }

func (t *DecideTool) Description() string {
	return "Ask the decision model to evaluate a factual context and pick exactly one option from a list. Use it for a fast second opinion, safety validation, classification, or yes/no confirmation."
}

func (t *DecideTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"state": map[string]any{
						"type":        "string",
						"description": "The factual context, code snippet, git diff, or situation being evaluated (text only).",
					},
					"question": map[string]any{
						"type":        "string",
						"description": "The discrete decision question to answer.",
					},
					"options": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
						},
						"description": "Candidate options to choose from (2 to 26 options, e.g. ['Yes', 'No'] or multiple categories).",
					},
				},
				"required": []string{"state", "question", "options"},
			},
		},
	}
}

func (t *DecideTool) Execute(ctx context.Context, params map[string]any) Result {
	if t == nil || t.decider == nil || !t.decider.Enabled() {
		return errResult("decision model is not configured or enabled")
	}

	state, _ := params["state"].(string)
	if strings.TrimSpace(state) == "" {
		return errResult("state is required")
	}
	question, _ := params["question"].(string)
	if strings.TrimSpace(question) == "" {
		return errResult("question is required")
	}

	rawOpts, ok := params["options"].([]any)
	if !ok || len(rawOpts) < decision.MinOptions {
		if strOpts, okStr := params["options"].([]string); okStr && len(strOpts) >= decision.MinOptions {
			rawOpts = make([]any, len(strOpts))
			for i, s := range strOpts {
				rawOpts[i] = s
			}
		} else {
			return errResult(fmt.Sprintf("options must be an array of at least %d items", decision.MinOptions))
		}
	}
	if len(rawOpts) > decision.MaxOptions {
		return errResult(fmt.Sprintf("at most %d options supported", decision.MaxOptions))
	}

	opts := make([]string, len(rawOpts))
	for i, o := range rawOpts {
		s, ok := o.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return errResult(fmt.Sprintf("option %d must be a non-empty string", i+1))
		}
		opts[i] = strings.TrimSpace(s)
	}

	resp, err := t.decider.Decide(ctx, decision.Request{
		State:    state,
		Question: question,
		Options:  opts,
	})
	if err != nil {
		return errResult(fmt.Sprintf("decision model decision failed: %v", err))
	}

	return Result{
		Output: fmt.Sprintf("Decision: %s. %s", resp.Choice, resp.Option),
	}
}
