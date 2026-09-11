package agent

import (
	"context"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func TestRunStopsOnRepeatedToolCallWithIdenticalOutput(t *testing.T) {
	// The client repeats a no-op Bash call forever; the agent must stop itself
	// instead of looping until the per-turn iteration cap.
	c := &coverageClient{reply: `<invoke name="Bash">{"command":"true"}</invoke>`}
	ag := newCoverageAgent(t, c, tui.DecisionAllowOnce, true)

	err := ag.Run(context.Background(), "loop please")
	if !IsNoProgressErr(err) {
		t.Fatalf("expected no-progress error, got %v", err)
	}
	if calls := int(c.chatCalls.Load()); calls > noProgressLimit+2 {
		t.Fatalf("loop guard fired too late: %d chat calls", calls)
	}
}

func TestNoProgressGuardResetsOnChangedOutput(t *testing.T) {
	g := new(noProgressGuard)
	call := "Read\x00{}"
	for i := 0; i < noProgressLimit-1; i++ {
		if _, loop := g.observe(call, "same"); loop {
			t.Fatalf("tripped early at %d", i)
		}
	}
	if _, loop := g.observe(call, "changed"); loop {
		t.Fatal("a changed output must reset the streak")
	}
}
