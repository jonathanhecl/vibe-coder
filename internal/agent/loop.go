package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/prompt"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

const (
	MaxIterations = 50
	MaxRetries    = 2
)

type Agent struct {
	cfg    *config.Config
	client ollama.Client
	reg    *tools.Registry
	perm   *permissions.Manager
	sess   *session.Session
	ui     uiPort
}

type uiPort interface {
	StartESCMonitor(interrupt func()) error
	StopESCMonitor()
	StreamAssistant(text string)
	EndAssistant()
	ShowToolCall(name string, params map[string]any)
	ShowToolResult(name, output string, isError bool)
	AskPermission(tool string, params map[string]any) tui.Decision
}

func New(
	cfg *config.Config,
	client ollama.Client,
	reg *tools.Registry,
	perm *permissions.Manager,
	sess *session.Session,
	ui uiPort,
) *Agent {
	return &Agent{
		cfg:    cfg,
		client: client,
		reg:    reg,
		perm:   perm,
		sess:   sess,
		ui:     ui,
	}
}

func (a *Agent) Run(rootCtx context.Context, userInput string) error {
	ctx, cancel := context.WithCancel(rootCtx)
	defer cancel()
	if err := a.ui.StartESCMonitor(cancel); err != nil {
		return err
	}
	defer a.ui.StopESCMonitor()

	a.sess.AddUser(userInput)

	if tasks, ok := detectParallelTasks(userInput); ok {
		tool := a.reg.Get("ParallelAgents")
		if tool != nil {
			params := map[string]any{"tasks": tasks}
			if a.perm.Check("ParallelAgents", params, a.ui) {
				a.ui.ShowToolCall("ParallelAgents", params)
				result := tool.Execute(ctx, params)
				a.ui.ShowToolResult("ParallelAgents", result.Output, result.IsError)
				a.sess.AddAssistant(result.Output)
				return nil
			}
		}
	}

	toolName, toolParams, wantsTool := inferSingleToolCall(userInput)
	for i := 0; i < MaxIterations; i++ {
		if wantsTool {
			tool := a.reg.Get(toolName)
			if tool == nil {
				return fmt.Errorf("tool not found: %s", toolName)
			}
			if !a.perm.Check(toolName, toolParams, a.ui) {
				a.sess.AddAssistant("Permission denied.")
				a.ui.EndAssistant()
				return nil
			}
			a.ui.ShowToolCall(toolName, toolParams)
			result := tool.Execute(ctx, toolParams)
			a.ui.ShowToolResult(toolName, result.Output, result.IsError)
			a.sess.AddAssistant(result.Output)
			// MVP-11 safety: infer one tool call once only.
			wantsTool = false
			continue
		}

		reply, err := a.chatOnce(ctx, userInput)
		if err != nil {
			return err
		}
		a.ui.StreamAssistant(reply)
		a.ui.EndAssistant()
		a.sess.AddAssistant(reply)
		return nil
	}
	return fmt.Errorf("iteration cap reached (%d)", MaxIterations)
}

var numberedTaskPattern = regexp.MustCompile(`(?m)^\s*(\d+[\.\)]\s+.+)$`)

func detectParallelTasks(input string) ([]any, bool) {
	matches := numberedTaskPattern.FindAllString(input, -1)
	if len(matches) >= 2 && len(matches) <= 4 {
		out := make([]any, 0, len(matches))
		for _, m := range matches {
			task := strings.TrimSpace(numberedTaskPattern.ReplaceAllString(m, "$1"))
			task = regexp.MustCompile(`^\d+[\.\)]\s*`).ReplaceAllString(task, "")
			if task == "" {
				continue
			}
			out = append(out, map[string]any{"prompt": task})
		}
		if len(out) >= 2 {
			return out, true
		}
	}

	lower := strings.ToLower(input)
	if strings.Contains(lower, " and ") {
		parts := strings.Split(input, " and ")
		if len(parts) >= 2 && len(parts) <= 4 {
			out := make([]any, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				out = append(out, map[string]any{"prompt": p})
			}
			if len(out) >= 2 {
				return out, true
			}
		}
	}
	return nil, false
}

func (a *Agent) chatOnce(rootCtx context.Context, userInput string) (string, error) {
	var lastErr error
	for attempt := 0; attempt <= MaxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(rootCtx, 2*time.Minute)
		systemPrompt := prompt.Build(a.cfg)
		stream, err := a.client.Chat(ctx, ollama.ChatRequest{
			Model: a.cfg.Model,
			Messages: []ollama.Message{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: userInput},
			},
			Stream: true,
			Options: ollama.ChatOptions{
				NumCtx:      a.cfg.ContextWindow,
				NumPredict:  a.cfg.MaxTokens,
				Temperature: a.cfg.Temperature,
			},
		})
		if err != nil {
			cancel()
			lastErr = err
		} else {
			var b strings.Builder
			for chunk := range stream {
				if chunk.Err != nil {
					if chunk.Err == context.Canceled {
						return "[Cancelled by user]", nil
					}
					lastErr = chunk.Err
					break
				}
				b.WriteString(chunk.Delta)
				if chunk.Done {
					cancel()
					return b.String(), nil
				}
			}
			if b.Len() > 0 && lastErr == nil {
				cancel()
				return b.String(), nil
			}
		}
		cancel()
		if attempt < MaxRetries {
			time.Sleep(time.Duration(1+attempt) * time.Second)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("empty assistant response")
	}
	return "", lastErr
}

func inferSingleToolCall(input string) (string, map[string]any, bool) {
	lower := strings.ToLower(input)
	if strings.Contains(lower, "using glob") || strings.Contains(lower, "use glob") {
		targetPath := "."
		if strings.Contains(lower, "./internal") || strings.Contains(lower, "internal") {
			targetPath = "./internal"
		}
		pattern := "*.go"
		if strings.Contains(lower, ".md") {
			pattern = "*.md"
		}
		return "Glob", map[string]any{
			"pattern": pattern,
			"path":    targetPath,
		}, true
	}
	return "", nil, false
}
