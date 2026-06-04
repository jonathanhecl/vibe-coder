package agent

import (
	"context"
	"fmt"
	"strings"
)

func (a *Agent) addRunContext(ctx context.Context, userInput string) {
	goal := resolveGoalForRun(a.sess, userInput)
	a.mu.Lock()
	a.currentGoal = goal
	a.mu.Unlock()
	a.sess.AddUser(userInput)

	if rag := a.getRAG(); rag != nil && a.cfg.RAG {
		k := a.cfg.RAGTopK
		if k <= 0 {
			k = 3
		}
		// RAG is additive context. If retrieval fails, the run can still
		// proceed with the user's request and normal tool access.
		if ctxText, err := rag.QueryText(ctx, userInput, k); err == nil && strings.TrimSpace(ctxText) != "" {
			a.sess.AddUser(ctxText)
		}
	}

	if w := a.getWatcher(); w != nil {
		if changes := w.PendingChanges(); len(changes) > 0 {
			a.sess.AddUser(w.Format(changes))
		}
	}
}

func (a *Agent) handleEmptyChatResponse(ctx context.Context, retries *int) (bool, error) {
	if a.hasPendingTodos() && *retries < 2 {
		*retries++
		a.sess.AddSystemNote("Model returned an empty response while TODO items remain. Retrying the next pending step.")
		a.compactBestEffort(ctx)
		return false, nil
	}
	if a.hasPendingTodos() {
		a.sess.AddSystemNote("Model returned repeated empty responses while TODO items remain; cannot complete the current run.")
		a.compactBestEffort(ctx)
		return true, fmt.Errorf("empty assistant response with pending todos")
	}
	a.sess.AddSystemNote("Model returned repeated empty responses; ending this run cleanly.")
	a.compactBestEffort(ctx)
	return true, nil
}

func (a *Agent) compactBestEffort(ctx context.Context) {
	// Best-effort context compaction; failures should not break the active turn.
	if !a.sess.ShouldCompact() {
		return
	}
	_ = a.sess.Compact(ctx, false)
}
func (a *Agent) BuildEmptyResponseRetryInput(baseInput string, repeatedState bool) string {
	baseInput = strings.TrimSpace(baseInput)
	var b strings.Builder
	b.WriteString(baseInput)
	b.WriteString("\n\n[retry_context]\n")
	b.WriteString("Previous attempt returned an empty assistant response. Continue from existing progress; do not restart solved steps.\n")

	if note := a.todoProgressNote(); note != "" {
		b.WriteString(note)
		b.WriteString("\n")
	}

	if completed := a.recentCompletedFileRuntimeNotes(3); len(completed) > 0 {
		b.WriteString("Recently completed file steps:\n")
		for _, item := range completed {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
	}

	if repeatedState {
		b.WriteString("State appears unchanged across retries. Pick the next pending TODO and call one concrete tool now.\n")
	}
	b.WriteString("[/retry_context]")
	return b.String()
}

func (a *Agent) recentCompletedFileRuntimeNotes(limit int) []string {
	if limit <= 0 {
		return nil
	}
	msgs := a.sess.MessagesReadOnly()
	out := make([]string, 0, limit)
	for i := len(msgs) - 1; i >= 0 && len(out) < limit; i-- {
		m := msgs[i]
		if m.Role != "assistant" {
			continue
		}
		text := strings.TrimSpace(m.Content)
		if !strings.HasPrefix(text, "[runtime] File written:") && !strings.HasPrefix(text, "[runtime] File updated:") {
			continue
		}
		text = strings.TrimPrefix(text, "[runtime] ")
		text = strings.TrimSpace(text)
		out = append(out, text)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
