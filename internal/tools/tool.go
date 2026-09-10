package tools

import "context"

type Tool interface {
	Name() string
	Description() string
	Schema() Schema
	Execute(ctx context.Context, params map[string]any) Result
}

type Effect uint8

const (
	EffectUnknown Effect = iota
	EffectRead
	EffectState
	EffectWrite
	EffectExecute
	EffectDelegate
)

func EffectOf(tool Tool) Effect {
	switch tool.(type) {
	case *ReadTool, *GlobTool, *GrepTool, *WebFetchTool, *WebSearchTool, *DescribeImageTool, *GitStatusTool, *GitDiffTool:
		return EffectRead
	case *TodoWriteTool, *TaskCreateTool, *TaskListTool, *TaskGetTool, *TaskUpdateTool, *AskUserQuestionTool:
		return EffectState
	case *WriteTool, *EditTool, *NotebookEditTool, *GitUndoTool:
		return EffectWrite
	case *BashTool, *InteractiveBashTool, *SendInputTool, *TerminateSessionTool:
		return EffectExecute
	case *SubAgentTool, *ParallelAgentsTool:
		return EffectDelegate
	default:
		return EffectUnknown
	}
}

type Executor func(context.Context, Tool, map[string]any) (Result, error)

type executorContextKey struct{}

func WithExecutor(ctx context.Context, executor Executor) context.Context {
	return context.WithValue(ctx, executorContextKey{}, executor)
}

func executorFromContext(ctx context.Context) Executor {
	executor, _ := ctx.Value(executorContextKey{}).(Executor)
	return executor
}

type Result struct {
	CallID        string
	Output        string
	HintsForModel string
	IsError       bool
	Diff          string // human-only diff for Edit/Write (not sent to model)
}

type Schema struct {
	Type     string         `json:"type"`
	Function FunctionSchema `json:"function"`
}

type FunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}
