package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/prompt"
	"github.com/jonathanhecl/vibe-coder/internal/skills"
)

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
	return "Run an isolated sub-agent prompt."
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
	maxTurns := asInt(params["max_turns"], 10)
	if maxTurns < 1 {
		maxTurns = 1
	}
	if maxTurns > 20 {
		maxTurns = 20
	}
	reply := ""
	nextPrompt := p
	for i := 0; i < maxTurns; i++ {
		out, err := t.runOneTurn(ctx, nextPrompt)
		if err != nil {
			return errResult(err.Error())
		}
		reply = out
		if strings.TrimSpace(out) == "" {
			break
		}
		nextPrompt = out
	}
	return Result{Output: reply}
}

func (t *SubAgentTool) runOneTurn(root context.Context, userPrompt string) (string, error) {
	ctx, cancel := context.WithTimeout(root, 2*time.Minute)
	defer cancel()
	stream, err := t.client.Chat(ctx, ollama.ChatRequest{
		Model: t.cfg.Model,
		Messages: []ollama.Message{
			{Role: "system", Content: buildSubAgentPrompt(t.cfg)},
			{Role: "user", Content: userPrompt},
		},
		Stream: true,
		Options: ollama.ChatOptions{
			NumCtx:      t.cfg.ContextWindow,
			NumPredict:  t.cfg.MaxTokens,
			Temperature: t.cfg.Temperature,
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

func buildSubAgentPrompt(cfg *config.Config) string {
	base := prompt.Build(cfg)
	block := skills.RenderBlock(skills.Load(cfg))
	if strings.TrimSpace(block) == "" {
		return base
	}
	return base + "\n\n# Loaded Skills\n" + block
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
		prompt string
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
		tasks = append(tasks, taskSpec{prompt: p})
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
			out, err := t.sub.runOneTurn(egCtx, tasks[i].prompt)
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
