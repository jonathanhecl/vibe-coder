package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// TestRecentProgressSummaryBoundsTokenBudget proves the progress summary handed
// to the decision model stays inside the token budget when the session holds
// far more system/tool output than the budget allows, and that it keeps the
// most recent content while dropping the oldest.
func TestRecentProgressSummaryBoundsTokenBudget(t *testing.T) {
	ag := newMissionAgent(t)

	// Each observation is ~100 tokens (400 ASCII bytes / 4).
	payload := strings.Repeat("x", 400)
	for i := 0; i < 60; i++ {
		ag.sess.AddToolResult("Bash", fmt.Sprintf("OLD-%03d %s", i, payload))
	}
	// The newest observation carries a unique marker that must survive.
	ag.sess.AddToolResult("Bash", "NEWEST "+payload)

	summary := ag.recentProgressSummary()

	if got := session.EstimateTokens(summary); got > progressSummaryTokenBudget {
		t.Fatalf("summary estimated at %d tokens, exceeds budget %d", got, progressSummaryTokenBudget)
	}
	if !strings.Contains(summary, "NEWEST") {
		t.Fatal("summary must keep the most recent tool output")
	}
	if strings.Contains(summary, "OLD-000") {
		t.Fatal("summary must drop the oldest tool output once the budget is exhausted")
	}
}

// TestRecentProgressSummaryEmitsChronologicalOrder keeps the surviving messages
// in their original order, not reversed by the newest-to-oldest walk.
func TestRecentProgressSummaryEmitsChronologicalOrder(t *testing.T) {
	ag := newMissionAgent(t)

	ag.sess.AddToolResult("Bash", "FIRST")
	ag.sess.AddToolResult("Bash", "SECOND")
	ag.sess.AddToolResult("Bash", "THIRD")

	summary := ag.recentProgressSummary()

	first := strings.Index(summary, "FIRST")
	second := strings.Index(summary, "SECOND")
	third := strings.Index(summary, "THIRD")
	if first < 0 || second < 0 || third < 0 {
		t.Fatalf("expected all small messages to survive, got %q", summary)
	}
	if !(first < second && second < third) {
		t.Fatalf("expected chronological order, got %q", summary)
	}
}

// TestRecentProgressSummaryKeepsCompletedTodos confirms the completed-todo
// section is untouched by the message budget.
func TestRecentProgressSummaryKeepsCompletedTodos(t *testing.T) {
	ag := newMissionAgent(t)
	ag.todoWriteTool().Store().Replace([]tools.TodoItem{
		{ID: "1", Content: "shipped the parser", Status: tools.TodoStatusCompleted},
		{ID: "2", Content: "pending thing", Status: tools.TodoStatusPending},
	})

	summary := ag.recentProgressSummary()

	if !strings.Contains(summary, "Completed task: shipped the parser") {
		t.Fatalf("summary must keep completed todos, got %q", summary)
	}
	if strings.Contains(summary, "pending thing") {
		t.Fatalf("summary must not include pending todos, got %q", summary)
	}
}

// TestRecentProgressSummaryClipsMessageBeyondBudget covers the degenerate case
// where a single message is larger than the whole budget: it is clipped to the
// budget instead of dropped, so the newest signal still reaches the judge.
func TestRecentProgressSummaryClipsMessageBeyondBudget(t *testing.T) {
	ag := newMissionAgent(t)
	ag.sess.AddToolResult("Bash", strings.Repeat("y", (progressSummaryTokenBudget+100)*4))

	summary := ag.recentProgressSummary()
	if summary == "" {
		t.Fatal("expected the oversized newest message to be clipped, not dropped")
	}
	if got := session.EstimateTokens(summary); got > progressSummaryTokenBudget+16 {
		t.Fatalf("clipped summary estimated at %d tokens, exceeds budget %d", got, progressSummaryTokenBudget)
	}
	if !strings.Contains(summary, "clipped") {
		t.Fatal("expected the clip marker in the summary")
	}
}
