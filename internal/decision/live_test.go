//go:build live

package decision

import (
	"context"
	"os"
	"testing"
	"time"
)

// Live tests exercise a real backend. Configure via env:
//   DECISION_LIVE_OLLAMA   Ollama base URL (e.g. http://mac-mini.local:11434)
//   DECISION_LIVE_MODEL    Ollama decision-capable model (default tev1:0.8b)
//   DECISION_LIVE_ENDPOINT custom System One server (e.g. http://mac-mini.local:8765)
// Each backend is skipped when its env var is unset.

func liveCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func exerciseDecider(t *testing.T, c *Client) {
	t.Helper()
	ctx := liveCtx(t)

	start := time.Now()
	info, err := c.Check(ctx)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	t.Logf("Check: backend=%s model=%s vision=%t (%v)", info.Backend, info.Model, info.Vision, time.Since(start))

	start = time.Now()
	b, err := c.DecideBool(ctx, "The sky is clear and the sun is shining.", "Is it raining?")
	t.Logf("DecideBool: %v err=%v (%v)", b, err, time.Since(start))
	if err != nil {
		t.Fatalf("DecideBool: %v", err)
	}

	start = time.Now()
	opt, idx, err := c.DecideChoice(ctx, "I was charged twice this month and want a refund.", "Which intent is this?", "billing_question", "technical_issue", "cancel_subscription")
	t.Logf("DecideChoice: %q idx=%d err=%v (%v)", opt, idx, err, time.Since(start))
	if err != nil {
		t.Fatalf("DecideChoice: %v", err)
	}

	start = time.Now()
	resp, err := c.Decide(ctx, Request{
		State:    "The checkout page returns HTTP 500 for every customer since the last deploy.",
		Question: "How urgent is this ticket?",
		Options:  []string{"not urgent", "normal", "urgent", "critical"},
	})
	t.Logf("Decide: %+v err=%v (%v)", resp, err, time.Since(start))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	start = time.Now()
	dangerous, err := c.IsCommandDangerous(ctx, "ls -la")
	t.Logf("IsCommandDangerous(ls -la): %v err=%v (%v)", dangerous, err, time.Since(start))
	if err != nil {
		t.Fatalf("IsCommandDangerous: %v", err)
	}

	start = time.Now()
	dangerous, err = c.IsCommandDangerous(ctx, "rm -rf /")
	t.Logf("IsCommandDangerous(rm -rf /): %v err=%v (%v)", dangerous, err, time.Since(start))
	if err != nil {
		t.Fatalf("IsCommandDangerous rm: %v", err)
	}

	start = time.Now()
	net, err := c.IsCommandNetworkAccess(ctx, "curl https://example.com")
	t.Logf("IsCommandNetworkAccess(curl): %v err=%v (%v)", net, err, time.Since(start))
	if err != nil {
		t.Fatalf("IsCommandNetworkAccess: %v", err)
	}

	start = time.Now()
	path, ok, err := c.DisambiguatePath(ctx, "the main entry point file", []string{"cmd/vibe/main.go", "internal/agent/agent.go", "README.md"})
	t.Logf("DisambiguatePath: %q ok=%v err=%v (%v)", path, ok, err, time.Since(start))
	if err != nil {
		t.Fatalf("DisambiguatePath: %v", err)
	}

	start = time.Now()
	done, err := c.CheckGoalCompletion(ctx, "Print hello world", "Ran `echo hello world`, output: hello world")
	t.Logf("CheckGoalCompletion: %v err=%v (%v)", done, err, time.Since(start))
	if err != nil {
		t.Fatalf("CheckGoalCompletion: %v", err)
	}

	start = time.Now()
	class, err := c.ClassifyFailure(ctx, "go build: undefined: fmt.Prinln")
	t.Logf("ClassifyFailure: %q err=%v (%v)", class, err, time.Since(start))
	if err != nil {
		t.Fatalf("ClassifyFailure: %v", err)
	}

	start = time.Now()
	ctype, err := c.ClassifyCommit(ctx, "fixed a nil pointer in config loading")
	t.Logf("ClassifyCommit: %q err=%v (%v)", ctype, err, time.Since(start))
	if err != nil {
		t.Fatalf("ClassifyCommit: %v", err)
	}

	start = time.Now()
	loop, err := c.CheckThinkingLoop(ctx, "I should check the file.\n\nI should check the file.\n\nI should check the file.")
	t.Logf("CheckThinkingLoop: %v err=%v (%v)", loop, err, time.Since(start))
	if err != nil {
		t.Fatalf("CheckThinkingLoop: %v", err)
	}

	start = time.Now()
	safety, err := c.AskSafety(ctx, "Bash", "ls -la")
	t.Logf("AskSafety: %+v err=%v (%v)", safety, err, time.Since(start))
	if err != nil {
		t.Fatalf("AskSafety: %v", err)
	}
}

func TestLiveOllama(t *testing.T) {
	ep := os.Getenv("DECISION_LIVE_OLLAMA")
	if ep == "" {
		t.Skip("DECISION_LIVE_OLLAMA not set")
	}
	model := os.Getenv("DECISION_LIVE_MODEL")
	if model == "" {
		model = "tev1:0.8b"
	}
	exerciseDecider(t, New(ep, model))
}

func TestLiveEndpoint(t *testing.T) {
	ep := os.Getenv("DECISION_LIVE_ENDPOINT")
	if ep == "" {
		t.Skip("DECISION_LIVE_ENDPOINT not set")
	}
	exerciseDecider(t, New(ep, ""))
}
