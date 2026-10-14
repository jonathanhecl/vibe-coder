package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/logger"
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
		if ctxText, err := a.retrieveContext(ctx, rag, userInput, k); err == nil && strings.TrimSpace(ctxText) != "" {
			a.sess.AddUser(ctxText)
		}
	}

	// Tiered memory: when a compaction demoted history since the last turn, or
	// the live transcript is already heavy, recall the demoted fragments most
	// related to the current goal instead of leaving them lost. Gated and
	// batched (one embedding call), so it is never a mandatory per-turn cost;
	// without an embedding role Recall returns nothing.
	if a.recallMemoryWanted() {
		k := a.cfg.RAGTopK
		if k <= 0 {
			k = 3
		}
		if block := a.sess.Recall(ctx, goal, k); strings.TrimSpace(block) != "" {
			a.sess.AddUser(block)
		}
	}

	if w := a.getWatcher(); w != nil {
		if changes := w.PendingChanges(); len(changes) > 0 {
			a.sess.AddUser(w.Format(changes))
		}
	}
}

// recallMemoryWanted reports whether recalling demoted history is worth an
// embedding call this turn: right after a compaction, or once the live
// transcript is past half the context window. It is a cheap gate (a flag and
// the session's running token estimate), so tiered memory never becomes a
// mandatory per-turn cost.
func (a *Agent) recallMemoryWanted() bool {
	if a.sess == nil || a.cfg == nil {
		return false
	}
	if a.compactedPending.Load() {
		return true
	}
	return a.cfg.ContextWindow > 0 && a.sess.TokenEstimate() > a.cfg.ContextWindow/2
}

func (a *Agent) handleEmptyChatResponse(ctx context.Context, retries *int) (bool, error) {
	if a.hasPendingTodos() && *retries < 2 {
		*retries++
		a.sess.AddRuntimeReminder("Model returned an empty response while TODO items remain. Retrying the next pending step.")
		a.compactBestEffort(ctx)
		return false, nil
	}
	if a.hasPendingTodos() {
		a.sess.AddRuntimeReminder("Model returned repeated empty responses while TODO items remain; cannot complete the current run.")
		a.compactBestEffort(ctx)
		return true, fmt.Errorf("empty assistant response with pending todos")
	}
	a.sess.AddSystemNote("Model returned repeated empty responses; ending this run cleanly.")
	a.compactBestEffort(ctx)
	return true, nil
}

func (a *Agent) compactBestEffort(ctx context.Context) {
	// Best-effort context compaction; failures should not break the active
	// turn. The trigger prices the *masked* transcript (what the wire
	// actually pays for) against the computed budget, so stale tool outputs
	// that the model no longer sees do not force early compaction.
	if a == nil || a.sess == nil || a.cfg == nil {
		return
	}
	hist := a.sess.MessagesReadOnly()
	if len(hist) == 0 {
		return
	}
	gen := a.sess.Generation()
	masked, _ := maskToolResults(hist, a.maskBeforeForState(hist, gen))
	budget := a.computeBudget(a.buildSystemPrompt())
	pct := a.cfg.Harness.CompactAtPercent
	if pct <= 0 {
		pct = 85
	}
	if transcriptCost(masked) <= budget.Transcript*pct/100 && len(hist) <= 300 {
		return
	}
	keepTailTokens := budget.Transcript * 40 / 100
	if err := a.sess.CompactTo(ctx, keepTailTokens); err != nil {
		logger.Errorf("context compaction failed: %v", err)
		return
	}
	if a.sess.Generation() == gen {
		// CompactTo was a no-op (nothing older than the kept tail).
		return
	}
	a.compactedPending.Store(true)
	if a.metrics != nil {
		a.metrics.NoteCompaction()
	}
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
