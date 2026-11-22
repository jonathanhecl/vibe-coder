package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/session"
)

// fillTranscript adds n user messages of roughly tokens tokens each (four
// ASCII bytes per estimated token) so the window arithmetic can be exercised
// with predictable sizes.
func fillTranscript(t *testing.T, ag *Agent, n, tokens int) {
	t.Helper()
	if tokens < 1 {
		t.Fatalf("token size must be positive, got %d", tokens)
	}
	body := strings.Repeat("x", tokens*4)
	for i := 0; i < n; i++ {
		ag.sess.AddUser(body)
	}
}

// TestWindowKeepsHistoryBelowCompactionThreshold is the regression guard for
// the silent band: the window used to keep only 60% of the transcript budget
// while compaction waited for 85%, so anything in between was cut from the
// wire with no summary. A transcript at ~68% of the budget must now reach the
// model whole.
func TestWindowKeepsHistoryBelowCompactionThreshold(t *testing.T) {
	t.Parallel()
	ag := newPromptBudgetAgent(t, true, true)
	budget := ag.computeBudget(ag.buildSystemPrompt())
	limit := ag.transcriptFullAt(budget.Transcript)
	if limit <= 0 {
		t.Fatalf("expected a positive transcript limit, got %d (transcript budget %d)", limit, budget.Transcript)
	}

	// 16 messages of 5% of the limit each: 80% of the limit, which is 68% of
	// the transcript budget — above the old 60% cutoff, well below the
	// compaction trigger.
	per := limit / 20
	if per < 1 {
		per = 1
	}
	fillTranscript(t, ag, 16, per)

	_, stats := ag.buildOllamaMessages(context.Background(), "SYS")
	if stats.MessagesDropped != 0 {
		t.Fatalf("dropped %d messages below the compaction threshold (limit %d, transcript est %d)", stats.MessagesDropped, limit, stats.TranscriptTokensEst)
	}
	if stats.MessagesSent != 16 {
		t.Fatalf("sent %d messages, want all 16", stats.MessagesSent)
	}
}

// TestWindowStillCutsBeyondCompactionThreshold keeps the window usable as a
// safety valve: a transcript past the threshold is trimmed, because compaction
// either already summarized it or could not run.
func TestWindowStillCutsBeyondCompactionThreshold(t *testing.T) {
	t.Parallel()
	ag := newPromptBudgetAgent(t, true, true)
	budget := ag.computeBudget(ag.buildSystemPrompt())
	limit := ag.transcriptFullAt(budget.Transcript)

	per := limit / 5
	if per < 1 {
		per = 1
	}
	fillTranscript(t, ag, 12, per)

	_, stats := ag.buildOllamaMessages(context.Background(), "SYS")
	if stats.MessagesDropped == 0 {
		t.Fatalf("expected the window to cut a transcript well past its limit %d", limit)
	}
	if stats.MessagesSent == 0 {
		t.Fatal("window must always keep at least the newest message")
	}
}

// TestTranscriptFullAtFollowsHarnessConfig pins the single source of truth the
// window and the compaction trigger share.
func TestTranscriptFullAtFollowsHarnessConfig(t *testing.T) {
	t.Parallel()
	ag := newPromptBudgetAgent(t, true, true)

	if got, want := ag.transcriptFullAt(1000), 1000*defaultCompactAtPercent/100; got != want {
		t.Fatalf("default limit = %d, want %d", got, want)
	}

	ag.cfg.Harness.CompactAtPercent = 60
	if got, want := ag.transcriptFullAt(1000), 600; got != want {
		t.Fatalf("configured limit = %d, want %d", got, want)
	}

	// The experimental session-history mode compacts sooner on purpose.
	ag.cfg.Harness.CompactAtPercent = 85
	ag.cfg.RAGMode = "context"
	if got, want := ag.transcriptFullAt(1000), 500; got != want {
		t.Fatalf("session-history RAG limit = %d, want %d", got, want)
	}

	if got := ag.transcriptFullAt(0); got != 0 {
		t.Fatalf("unknown budget must report a zero limit, got %d", got)
	}
}

// TestContextPressureReportsUsedAndLimit covers what /stats prints: the masked
// transcript cost against the same limit compaction uses.
func TestContextPressureReportsUsedAndLimit(t *testing.T) {
	t.Parallel()
	ag := newPromptBudgetAgent(t, true, true)
	budget := ag.computeBudget(ag.buildSystemPrompt())
	wantLimit := ag.transcriptFullAt(budget.Transcript)

	if used, limit := ag.ContextPressure(); used != 0 || limit != 0 {
		t.Fatalf("empty session reported used=%d limit=%d, want 0/0", used, limit)
	}

	fillTranscript(t, ag, 4, 50)
	used, limit := ag.ContextPressure()
	if limit != wantLimit {
		t.Fatalf("limit = %d, want %d", limit, wantLimit)
	}
	if wantUsed := 4 * session.EstimateTokens(strings.Repeat("x", 200)); used != wantUsed {
		t.Fatalf("used = %d, want %d", used, wantUsed)
	}
	if used >= limit {
		t.Fatalf("used %d unexpectedly at the limit %d", used, limit)
	}
}
