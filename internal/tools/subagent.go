package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/prompt"
	"github.com/jonathanhecl/vibe-coder/internal/skills"
)

const (
	defaultSubAgentTurns = 5
	maxSubAgentTurns     = 10
	// maxSubAgentInvokesPerTurn bounds how many XML invokes are honored
	// from a single sub-agent reply. The main loop allows more; the
	// sub-agent stays small on purpose.
	maxSubAgentInvokesPerTurn = 3
	// maxSubAgentObservationChars truncates one tool output before it is
	// fed back into the sub-agent transcript.
	maxSubAgentObservationChars = 6000
)

var subAgentInvokeRe = regexp.MustCompile(`(?is)<invoke[^>]*name=["']([A-Za-z0-9_:-]+)["'][^>]*>`)

type SubAgentTool struct {
	cfg    *config.Config
	client ollama.Client
}

type ParallelAgentsTool struct {
	sub *SubAgentTool
}

func NewSubAgentTool(cfg *config.Config, client ollama.Client) *SubAgentTool {
	return &SubAgentTool{cfg: cfg, client: client}
}

func NewParallelAgentsTool(sub *SubAgentTool) *ParallelAgentsTool {
	return &ParallelAgentsTool{sub: sub}
}

func (t *SubAgentTool) Name() string { return "SubAgent" }
func (t *SubAgentTool) Description() string {
	return "Run an isolated sub-agent with file-search tools (Read, Glob, Grep) for up to max_turns iterations. Set allow_writes=true to also allow Write, Edit, Bash."
}
func (t *SubAgentTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt":       map[string]any{"type": "string"},
					"max_turns":    map[string]any{"type": "integer"},
					"allow_writes": map[string]any{"type": "boolean"},
				},
				"required": []string{"prompt"},
			},
		},
	}
}

func (t *SubAgentTool) Execute(ctx context.Context, params map[string]any) Result {
	p, _ := params["prompt"].(string)
	if strings.TrimSpace(p) == "" {
		return errResult("prompt is required")
	}
	out, err := t.runLoop(ctx, p, subAgentTurns(params), subAgentAllowWrites(params))
	if err != nil {
		return errResult(err.Error())
	}
	return Result{Output: out}
}

// runLoop runs a bounded ReAct loop over a safe tool subset. Read-only tools
// are always available; write tools are added only when allowWrites is true.
func (t *SubAgentTool) runLoop(ctx context.Context, userPrompt string, turns int, allowWrites bool) (string, error) {
	if turns < 1 {
		turns = defaultSubAgentTurns
	}
	if turns > maxSubAgentTurns {
		turns = maxSubAgentTurns
	}
	allowed := map[string]Tool{
		"Read": NewReadTool(),
		"Glob": NewGlobTool(),
		"Grep": NewGrepTool(),
	}
	if allowWrites {
		allowed["Write"] = NewWriteTool()
		allowed["Edit"] = NewEditTool()
		allowed["Bash"] = NewBashTool()
	}
	messages := []ollama.Message{
		{Role: "system", Content: buildSubAgentPrompt(t.cfg, allowWrites, allowed)},
		{Role: "user", Content: userPrompt},
	}
	lastReply := ""
	for turn := 0; turn < turns; turn++ {
		reply, err := t.chat(ctx, messages)
		if err != nil {
			return "", err
		}
		lastReply = reply
		messages = append(messages, ollama.Message{Role: "assistant", Content: reply})
		calls := parseSubAgentInvokes(reply, maxSubAgentInvokesPerTurn)
		if len(calls) == 0 {
			return reply, nil
		}
		var obs strings.Builder
		for _, c := range calls {
			tool, ok := allowed[c.name]
			if !ok {
				fmt.Fprintf(&obs, "[tool_result name=%s]\nError: tool %q is not allowed in this sub-agent.\n[/tool_result]\n", c.name, c.name)
				continue
			}
			res := tool.Execute(ctx, c.params)
			out := res.Output
			if len(out) > maxSubAgentObservationChars {
				out = out[:maxSubAgentObservationChars] + "\n... (truncated) ..."
			}
			if strings.TrimSpace(out) == "" {
				out = "(no output)"
			}
			fmt.Fprintf(&obs, "[tool_result name=%s]\n%s\n[/tool_result]\n", c.name, out)
		}
		note := strings.TrimSpace(obs.String())
		if note == "" {
			note = "(no tool output)"
		}
		messages = append(messages, ollama.Message{Role: "user", Content: note})
	}
	if strings.TrimSpace(lastReply) == "" {
		return "", fmt.Errorf("sub-agent produced no answer in %d turns", turns)
	}
	return lastReply + "\n\n[Sub-agent reached its turn limit; treat partial findings above as final.]", nil
}

// runOneTurn keeps a single-shot helper for callers that want one model
// pass without tools (e.g. summarization-style fan-out).
func (t *SubAgentTool) runOneTurn(root context.Context, userPrompt string) (string, error) {
	return t.runLoop(root, userPrompt, 1, false)
}

func (t *SubAgentTool) chat(ctx context.Context, messages []ollama.Message) (string, error) {
	model, numCtx, numPredict, temperature := subAgentChatSettings(t.cfg)
	stream, err := t.client.Chat(ctx, ollama.ChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   true,
		Options: ollama.ChatOptions{
			NumCtx:      numCtx,
			NumPredict:  numPredict,
			Temperature: temperature,
		},
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for chunk := range stream {
		if chunk.Err != nil {
			return "", chunk.Err
		}
		b.WriteString(chunk.Delta)
		if chunk.Done {
			break
		}
	}
	return b.String(), nil
}

func subAgentChatSettings(cfg *config.Config) (string, int, int, float64) {
	if cfg == nil {
		return "", 0, 0, 0
	}
	return cfg.Model, cfg.ContextWindow, cfg.MaxTokens, cfg.Temperature
}

func subAgentTurns(params map[string]any) int {
	switch v := params["max_turns"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return defaultSubAgentTurns
	}
}

func subAgentAllowWrites(params map[string]any) bool {
	v, _ := params["allow_writes"].(bool)
	return v
}

func buildSubAgentPrompt(cfg *config.Config, allowWrites bool, allowed map[string]Tool) string {
	base := prompt.Build(cfg)
	block := skills.RenderBlock(skills.Load(cfg))
	if strings.TrimSpace(block) != "" {
		base = base + "\n\n# Loaded Skills\n" + block
	}
	names := make([]string, 0, len(allowed))
	for name := range allowed {
		names = append(names, name)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n# Sub-agent scope\n")
	b.WriteString("You are an isolated research sub-agent. Answer the delegated prompt directly.\n")
	if allowWrites {
		b.WriteString("You may use these tools: " + strings.Join(names, ", ") + ".\n")
	} else {
		b.WriteString("You are read-only. Use only: " + strings.Join(names, ", ") + ". Do not edit files or run commands.\n")
	}
	b.WriteString("To call a tool, reply with XML blocks: <invoke name=\"ToolName\">{\"param\":\"value\"}</invoke> (up to 3 per reply). ")
	b.WriteString("A reply with no tool call is your final answer.")
	return b.String()
}

type subAgentCall struct {
	name   string
	params map[string]any
}

// parseSubAgentInvokes extracts up to maxCalls <invoke> envelopes in reply
// order. It is a local copy of the agent XML parser to avoid an import
// cycle (agent imports tools, so tools cannot import agent).
func parseSubAgentInvokes(reply string, maxCalls int) []subAgentCall {
	if maxCalls <= 0 || !strings.Contains(strings.ToLower(reply), "</") {
		return nil
	}
	var out []subAgentCall
	for _, loc := range subAgentInvokeRe.FindAllStringSubmatchIndex(reply, -1) {
		if len(loc) < 4 || len(out) >= maxCalls {
			break
		}
		name := strings.TrimSpace(reply[loc[2]:loc[3]])
		body, _, ok := scanBalancedJSON(reply, loc[1])
		if !ok {
			continue
		}
		params := map[string]any{}
		if err := json.Unmarshal([]byte(body), &params); err != nil {
			continue
		}
		out = append(out, subAgentCall{name: name, params: params})
	}
	return out
}

// scanBalancedJSON returns the first balanced JSON object at or after start.
func scanBalancedJSON(s string, start int) (string, int, bool) {
	i := start
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	if i >= len(s) || s[i] != '{' {
		return "", i, false
	}
	depth := 0
	inString := false
	escaped := false
	for j := i; j < len(s); j++ {
		c := s[j]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[i : j+1], j + 1, true
			}
		}
	}
	return "", len(s), false
}

func (t *ParallelAgentsTool) Name() string { return "ParallelAgents" }
func (t *ParallelAgentsTool) Description() string {
	return "Run 2-4 sub-agent tasks in parallel."
}
func (t *ParallelAgentsTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tasks": map[string]any{"type": "array"},
				},
				"required": []string{"tasks"},
			},
		},
	}
}

func (t *ParallelAgentsTool) Execute(ctx context.Context, params map[string]any) Result {
	taskItems, ok := params["tasks"].([]any)
	if !ok || len(taskItems) < 2 || len(taskItems) > 4 {
		return errResult("tasks must contain 2 to 4 items")
	}

	type taskSpec struct {
		prompt      string
		maxTurns    int
		allowWrites bool
	}
	tasks := make([]taskSpec, 0, len(taskItems))
	for _, item := range taskItems {
		taskMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		p, _ := taskMap["prompt"].(string)
		if strings.TrimSpace(p) == "" {
			continue
		}
		turns := defaultSubAgentTurns
		switch v := taskMap["max_turns"].(type) {
		case float64:
			turns = int(v)
		case int:
			turns = v
		}
		allowWrites, _ := taskMap["allow_writes"].(bool)
		tasks = append(tasks, taskSpec{prompt: p, maxTurns: turns, allowWrites: allowWrites})
	}
	if len(tasks) < 2 {
		return errResult("at least two valid task prompts are required")
	}

	results := make([]string, len(tasks))
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(4)
	for i := range tasks {
		i := i
		eg.Go(func() error {
			out, err := t.sub.runLoop(egCtx, tasks[i].prompt, tasks[i].maxTurns, tasks[i].allowWrites)
			if err != nil {
				return err
			}
			results[i] = out
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return errResult(fmt.Sprintf("parallel execution failed: %v", err))
	}
	return Result{Output: strings.Join(results, "\n\n---\n\n")}
}
