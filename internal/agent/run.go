package agent

import (
	"context"
	"fmt"

	"github.com/jonathanhecl/vibe-coder/internal/terminal"
)

type toolExecutionMode struct {
	// The inferred-tool and XML-fallback paths historically surfaced denial
	// differently. Keep that UI detail explicit while sharing execution logic.
	showPermissionDeniedResult bool
	endAssistantOnDenied       bool
}

func (a *Agent) Run(rootCtx context.Context, userInput string) error {
	ctx, cancel := context.WithCancel(rootCtx)
	defer cancel()
	defer terminal.DefaultManager().TerminateAll()
	if err := a.ui.StartESCMonitor(cancel); err != nil {
		return err
	}
	defer a.ui.StopESCMonitor()

	a.addRunContext(ctx, userInput)
	a.resetTodoNoteDedup()

	// ParallelAgents is explicit-only: the model calls it when a task is
	// genuinely decomposable. Previous auto-detection split any message
	// containing " and " or numbered lines, hijacking normal requests.
	toolName, toolParams, wantsTool := inferSingleToolCall(userInput)
	thinkingOnlyRetries := 0
	emptyChatErrRetries := 0
	for i := 0; i < MaxIterations; i++ {
		if wantsTool {
			tool := a.reg.Get(toolName)
			if tool == nil {
				return fmt.Errorf("tool not found: %s", toolName)
			}
			if _, executed, err := a.executeTool(ctx, tool, toolName, toolParams, toolExecutionMode{
				endAssistantOnDenied: true,
			}); err != nil {
				return err
			} else if !executed {
				return nil
			}
			a.compactBestEffort(ctx)
			// MVP-11 safety: infer one tool call once only.
			wantsTool = false
			continue
		}

		a.addTodoProgressNoteIfChanged()
		reply, err := a.chatOnce(ctx)
		if err != nil {
			if IsEmptyAssistantResponseErr(err) {
				done, err := a.handleEmptyChatResponse(ctx, &emptyChatErrRetries)
				if done {
					return err
				}
				continue
			}
			return err
		}
		emptyChatErrRetries = 0
		if calls := parseXMLFallbackAll(reply, MaxToolsPerReply); len(calls) > 0 {
			thinkingOnlyRetries = 0
			// Verify every tool exists before executing any, so a reply
			// mixing known and unknown envelopes falls back to a final
			// answer instead of a confusing partial execution.
			unknown := ""
			for _, c := range calls {
				if a.reg.Get(c.Name) == nil {
					unknown = c.Name
					break
				}
			}
			if unknown != "" {
				a.sess.AddAssistant(reply)
				a.compactBestEffort(ctx)
				return nil
			}
			if assistantVisibleText(reply) == "" {
				a.ui.CollapseAssistantOutput()
			}
			a.sess.AddAssistant(reply)
			for _, c := range calls {
				tool := a.reg.Get(c.Name)
				_, executed, err := a.executeTool(ctx, tool, c.Name, c.Params, toolExecutionMode{
					showPermissionDeniedResult: true,
				})
				if err != nil {
					return err
				}
				if !executed {
					return nil
				}
			}
			a.compactBestEffort(ctx)
			continue
		}
		if assistantVisibleText(reply) == "" {
			if thinkingOnlyRetries < 2 {
				thinkingOnlyRetries++
				continue
			}
			thinkingOnlyRetries = 0
			if a.hasPendingTodos() {
				a.sess.AddSystemNote("Model reply was empty while TODO items remain. Continue with the next pending step via a tool call; do not re-investigate completed steps.")
				a.compactBestEffort(ctx)
				continue
			}
			a.sess.AddSystemNote("Model reply was empty; ending this run without a visible assistant message.")
			a.compactBestEffort(ctx)
			return nil
		}
		thinkingOnlyRetries = 0
		if a.hasPendingTodos() {
			a.sess.AddAssistant(reply)
			a.sess.AddSystemNote("There are still pending TODO items. Continue executing the remaining steps with tool calls — do not finish the turn with plain text.")
			a.compactBestEffort(ctx)
			continue
		}
		a.sess.AddAssistant(reply)
		a.compactBestEffort(ctx)
		return nil
	}
	return fmt.Errorf("iteration cap reached (%d)", MaxIterations)
}
