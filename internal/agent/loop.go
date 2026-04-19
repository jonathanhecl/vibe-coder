package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	gitx "github.com/jonathanhecl/vibe-coder/internal/git"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/prompt"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/skills"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
	"github.com/jonathanhecl/vibe-coder/internal/watcher"
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

	mu       sync.RWMutex
	planMode bool
	watcher  *watcher.Watcher
	cp       *gitx.Checkpoint
	autoTest *gitx.AutoTest
	rag      ragProvider
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

type ragProvider interface {
	QueryText(ctx context.Context, query string, k int) (string, error)
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
		cfg:      cfg,
		client:   client,
		reg:      reg,
		perm:     perm,
		sess:     sess,
		ui:       ui,
		cp:       gitx.NewCheckpoint(cfg.Cwd),
		autoTest: gitx.NewAutoTest(cfg.Cwd),
	}
}

func (a *Agent) SetRAG(r ragProvider) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rag = r
}

func (a *Agent) SetWatcher(w *watcher.Watcher) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.watcher = w
}

func (a *Agent) EnterPlanMode() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.planMode = true
}

func (a *Agent) ExitPlanMode() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.planMode = false
}

func (a *Agent) InPlanMode() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.planMode
}

func (a *Agent) Run(rootCtx context.Context, userInput string) error {
	ctx, cancel := context.WithCancel(rootCtx)
	defer cancel()
	if err := a.ui.StartESCMonitor(cancel); err != nil {
		return err
	}
	defer a.ui.StopESCMonitor()

	a.sess.AddUser(userInput)

	if rag := a.getRAG(); rag != nil && a.cfg.RAG {
		k := a.cfg.RAGTopK
		if k <= 0 {
			k = 3
		}
		if ctxText, err := rag.QueryText(ctx, userInput, k); err == nil && strings.TrimSpace(ctxText) != "" {
			a.sess.AddUser(ctxText)
		}
	}

	if w := a.getWatcher(); w != nil {
		if changes := w.PendingChanges(); len(changes) > 0 {
			a.sess.AddUser(w.Format(changes))
		}
	}

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
			if a.InPlanMode() && toolName == "Write" && !a.isWriteAllowedInPlan(toolParams) {
				blockMsg := "Write blocked in plan mode. Allowed path: <cwd>/.vibe-coder/plans/"
				a.ui.ShowToolResult(toolName, blockMsg, true)
				a.sess.AddAssistant(blockMsg)
				return nil
			}
			if !a.perm.Check(toolName, toolParams, a.ui) {
				a.sess.AddAssistant("Permission denied.")
				a.ui.EndAssistant()
				return nil
			}
			if toolName == "Write" || toolName == "Edit" {
				if err := a.cp.Create("pre-edit"); err != nil {
					return err
				}
			}
			a.ui.ShowToolCall(toolName, toolParams)
			result := tool.Execute(ctx, toolParams)
			a.ui.ShowToolResult(toolName, result.Output, result.IsError)
			a.sess.AddAssistant(result.Output)
			if !result.IsError && (toolName == "Write" || toolName == "Edit") {
				if w := a.getWatcher(); w != nil {
					w.RefreshSnapshot()
				}
				if auto := a.autoTest.RunAfterEdit(ctx, asString(toolParams["file_path"])); strings.TrimSpace(auto) != "" {
					a.ui.ShowToolResult("AUTO-TEST", auto, true)
					a.sess.AddAssistant(auto)
				}
			}
			_ = a.sess.Compact(ctx, false)
			// MVP-11 safety: infer one tool call once only.
			wantsTool = false
			continue
		}

		reply, err := a.chatOnce(ctx, userInput)
		if err != nil {
			return err
		}
		if toolName, toolParams, ok := parseXMLFallback(reply); ok {
			tool := a.reg.Get(toolName)
			if tool == nil {
				a.ui.StreamAssistant(reply)
				a.ui.EndAssistant()
				a.sess.AddAssistant(reply)
				_ = a.sess.Compact(ctx, false)
				return nil
			}
			if a.InPlanMode() && toolName == "Write" && !a.isWriteAllowedInPlan(toolParams) {
				blockMsg := "Write blocked in plan mode. Allowed path: <cwd>/.vibe-coder/plans/"
				a.ui.ShowToolResult(toolName, blockMsg, true)
				a.sess.AddAssistant(blockMsg)
				return nil
			}
			if !a.perm.Check(toolName, toolParams, a.ui) {
				deny := "Permission denied."
				a.ui.ShowToolResult(toolName, deny, true)
				a.sess.AddAssistant(deny)
				return nil
			}
			if toolName == "Write" || toolName == "Edit" {
				if err := a.cp.Create("pre-edit"); err != nil {
					return err
				}
			}
			a.ui.ShowToolCall(toolName, toolParams)
			result := tool.Execute(ctx, toolParams)
			a.ui.ShowToolResult(toolName, result.Output, result.IsError)
			a.sess.AddAssistant(result.Output)
			if !result.IsError && (toolName == "Write" || toolName == "Edit") {
				if w := a.getWatcher(); w != nil {
					w.RefreshSnapshot()
				}
				if auto := a.autoTest.RunAfterEdit(ctx, asString(toolParams["file_path"])); strings.TrimSpace(auto) != "" {
					a.ui.ShowToolResult("AUTO-TEST", auto, true)
					a.sess.AddAssistant(auto)
				}
			}
			userInput = "Tool result:\n" + result.Output
			_ = a.sess.Compact(ctx, false)
			continue
		}
		a.ui.StreamAssistant(reply)
		a.ui.EndAssistant()
		a.sess.AddAssistant(reply)
		_ = a.sess.Compact(ctx, false)
		return nil
	}
	return fmt.Errorf("iteration cap reached (%d)", MaxIterations)
}

func (a *Agent) getWatcher() *watcher.Watcher {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.watcher
}

func (a *Agent) getRAG() ragProvider {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.rag
}

func (a *Agent) isWriteAllowedInPlan(params map[string]any) bool {
	rawPath, _ := params["file_path"].(string)
	if strings.TrimSpace(rawPath) == "" {
		return false
	}
	absPath := rawPath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(a.cfg.Cwd, absPath)
	}
	resolvedPath := absPath
	if v, err := filepath.EvalSymlinks(absPath); err == nil && strings.TrimSpace(v) != "" {
		resolvedPath = v
	}

	allowedRoot := filepath.Join(a.cfg.Cwd, ".vibe-coder", "plans")
	_ = os.MkdirAll(allowedRoot, 0o755)
	resolvedRoot := allowedRoot
	if v, err := filepath.EvalSymlinks(allowedRoot); err == nil && strings.TrimSpace(v) != "" {
		resolvedRoot = v
	}

	pathAbs, err := filepath.Abs(resolvedPath)
	if err != nil {
		return false
	}
	rootAbs, err := filepath.Abs(resolvedRoot)
	if err != nil {
		return false
	}
	if pathAbs == rootAbs {
		return true
	}
	return strings.HasPrefix(pathAbs, rootAbs+string(filepath.Separator))
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
		skillsBlock := skills.RenderBlock(skills.Load(a.cfg))
		if skillsBlock != "" {
			systemPrompt = systemPrompt + "\n\n# Loaded Skills\n" + skillsBlock
		}
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
			if strings.Contains(strings.ToLower(err.Error()), "model not found") {
				if pulled := a.tryAutoPullModel(rootCtx); pulled {
					attempt--
					continue
				}
			}
			lastErr = err
		} else {
			var b strings.Builder
			for chunk := range stream {
				if chunk.Err != nil {
					if chunk.Err == context.Canceled {
						cancel()
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

func (a *Agent) tryAutoPullModel(ctx context.Context) bool {
	allow := a.perm.Check("Bash", map[string]any{
		"command": "ollama pull " + a.cfg.Model,
	}, a.ui)
	if !allow {
		return false
	}
	pullErr := a.client.Pull(ctx, a.cfg.Model, func(ev ollama.PullEvent) {
		progress := ev.Status
		if ev.Total > 0 {
			progress = fmt.Sprintf("%s (%d/%d)", ev.Status, ev.Completed, ev.Total)
		}
		a.ui.ShowToolResult("PullModel", progress, false)
	})
	return pullErr == nil
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

func asString(v any) string {
	s, _ := v.(string)
	return s
}
