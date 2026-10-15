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
	case *ReadTool, *ReadManyTool, *GlobTool, *GrepTool, *WebFetchTool, *WebSearchTool, *DescribeImageTool, *ScreenshotTool, *SkillTool, *GitStatusTool, *GitDiffTool, *DecideTool,
		*DiagnosticsTool, *BackgroundStatusTool, *BackgroundListTool:
		return EffectRead
	case *TodoWriteTool, *TaskCreateTool, *TaskListTool, *TaskGetTool, *TaskUpdateTool, *AskUserQuestionTool,
		*MissionStartTool, *MissionCompleteTool, *MissionBlockedTool:
		return EffectState
	case *WriteTool, *EditTool, *NotebookEditTool, *GitUndoTool:
		return EffectWrite
	case *BashTool, *InteractiveBashTool, *SendInputTool, *TerminateSessionTool, *HTTPRequestTool,
		*BackgroundRunTool, *BackgroundKillTool:
		return EffectExecute
	case *SubAgentTool, *ParallelAgentsTool:
		return EffectDelegate
	default:
		return EffectUnknown
	}
}

// IsAllowedInMode reports whether tool can ever be used under the active mode
// flags. Tools that are unconditionally blocked in reviewMode, planMode, or
// offline mode are omitted from wire schemas and XML tool catalogues to save
// tokens and prevent models from attempting forbidden calls.
func IsAllowedInMode(tool Tool, planMode, reviewMode, offline bool) bool {
	if tool == nil {
		return false
	}
	if offline {
		switch tool.(type) {
		case *WebFetchTool, *WebSearchTool, *HTTPRequestTool:
			return false
		}
	}
	effect := EffectOf(tool)
	safe := effect == EffectRead || effect == EffectState || effect == EffectDelegate
	if reviewMode {
		return safe
	}
	if planMode {
		if safe {
			return true
		}
		switch tool.(type) {
		case *WriteTool, *EditTool:
			return true
		default:
			return false
		}
	}
	return true
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
