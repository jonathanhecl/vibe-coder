package permissions

import (
	"context"
	"errors"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/decision"
)

// countingSafetyDecider implements the AskSafety path and counts how many times
// the decision model was actually consulted, so a cache hit is observable.
type countingSafetyDecider struct {
	enabled    bool
	calls      int
	action     string
	confidence float64
	err        error
	nilResult  bool
}

func (d *countingSafetyDecider) Enabled() bool { return d.enabled }

func (d *countingSafetyDecider) IsCommandDangerous(context.Context, string) (bool, error) {
	return false, nil
}

func (d *countingSafetyDecider) AskSafety(context.Context, string, string) (*decision.SafetyDecision, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	if d.nilResult {
		return nil, nil
	}
	return &decision.SafetyDecision{Action: d.action, Confidence: d.confidence}, nil
}

// countingActionDecider is a binary decider (no AskSafety) that counts
// IsActionDangerous calls to observe the action-path cache.
type countingActionDecider struct {
	enabled bool
	calls   int
	danger  bool
}

func (d *countingActionDecider) Enabled() bool { return d.enabled }

func (d *countingActionDecider) IsCommandDangerous(context.Context, string) (bool, error) {
	return false, nil
}

func (d *countingActionDecider) IsActionDangerous(context.Context, string, string) (bool, error) {
	d.calls++
	return d.danger, nil
}

func newAssistedManager(dec SafetyDecider) *Manager {
	m := NewManager(&config.Config{YesMode: false, DecisionModel: "decision-model", AssistedYes: true})
	m.SetSafetyDecider(dec)
	return m
}

// TestAssistedVerdictCache_HitSkipsDecider proves that an identical command
// consults the decision model only once and still returns the same approval.
func TestAssistedVerdictCache_HitSkipsDecider(t *testing.T) {
	dec := &countingSafetyDecider{enabled: true, action: "allow", confidence: 0.95}
	m := newAssistedManager(dec)

	for i := 0; i < 3; i++ {
		if !m.Check("Bash", map[string]any{"command": "make build"}, nil) {
			t.Fatalf("call %d: expected the command to be auto-approved", i)
		}
	}
	if dec.calls != 1 {
		t.Fatalf("expected the decider to be consulted once, got %d", dec.calls)
	}
}

// TestAssistedVerdictCache_BlockedVerdictReplayed proves a memoized "block"
// verdict reproduces the original outcome (still prompts, never auto-approved).
func TestAssistedVerdictCache_BlockedVerdictReplayed(t *testing.T) {
	dec := &countingSafetyDecider{enabled: true, action: "block", confidence: 0.99}
	m := newAssistedManager(dec)

	for i := 0; i < 3; i++ {
		if m.Check("Bash", map[string]any{"command": "make install"}, nil) {
			t.Fatalf("call %d: a blocked verdict must never auto-approve", i)
		}
	}
	if dec.calls != 1 {
		t.Fatalf("expected the decider to be consulted once, got %d", dec.calls)
	}
}

// TestAssistedVerdictCache_DifferentInputMisses proves the key includes the
// command, so a different command is judged again.
func TestAssistedVerdictCache_DifferentInputMisses(t *testing.T) {
	dec := &countingSafetyDecider{enabled: true, action: "allow", confidence: 0.95}
	m := newAssistedManager(dec)

	if !m.Check("Bash", map[string]any{"command": "make build"}, nil) {
		t.Fatal("expected the first command to be auto-approved")
	}
	if !m.Check("Bash", map[string]any{"command": "make test"}, nil) {
		t.Fatal("expected the second command to be auto-approved")
	}
	if dec.calls != 2 {
		t.Fatalf("expected the decider to be consulted twice for distinct commands, got %d", dec.calls)
	}
}

// TestAssistedVerdictCache_ErrorNotCached proves a failing decision call is
// never memoized: once the decider recovers the model is asked again.
func TestAssistedVerdictCache_ErrorNotCached(t *testing.T) {
	dec := &countingSafetyDecider{enabled: true, action: "allow", confidence: 0.95}
	m := newAssistedManager(dec)

	dec.err = errors.New("connection refused")
	if m.Check("Bash", map[string]any{"command": "make build"}, nil) {
		t.Fatal("expected the failing decider to fall through to a prompt")
	}
	if m.AssistedMode() {
		t.Fatal("expected assisted mode to deactivate on a decider error")
	}

	dec.err = nil
	m.SetAssistedMode(true)
	if !m.Check("Bash", map[string]any{"command": "make build"}, nil) {
		t.Fatal("expected approval once the decider recovers")
	}
	if dec.calls != 2 {
		t.Fatalf("expected the failed verdict not to be cached (2 calls), got %d", dec.calls)
	}
}

// TestAssistedVerdictCache_UnavailableNotCached proves a nil decision (a
// decider that answered nothing) is treated as unavailable and not memoized.
func TestAssistedVerdictCache_UnavailableNotCached(t *testing.T) {
	dec := &countingSafetyDecider{enabled: true, nilResult: true}
	m := newAssistedManager(dec)

	for i := 0; i < 2; i++ {
		if m.Check("Bash", map[string]any{"command": "make build"}, nil) {
			t.Fatalf("call %d: a nil verdict must not auto-approve", i)
		}
	}
	if dec.calls != 2 {
		t.Fatalf("expected a nil verdict not to be cached (2 calls), got %d", dec.calls)
	}
}

// TestAssistedVerdictCache_ActionPath proves the action path (non-shell tool)
// is memoized too.
func TestAssistedVerdictCache_ActionPath(t *testing.T) {
	dec := &countingActionDecider{enabled: true, danger: false}
	m := newAssistedManager(dec)

	for i := 0; i < 3; i++ {
		if !m.Check("Write", map[string]any{"file_path": "notes.md", "contents": "hi"}, nil) {
			t.Fatalf("call %d: expected the Write to be auto-approved", i)
		}
	}
	if dec.calls != 1 {
		t.Fatalf("expected the action decider to be consulted once, got %d", dec.calls)
	}
}

// TestVerdictCache_EvictsOldestBeyondCapacity verifies the bounded FIFO
// eviction directly.
func TestVerdictCache_EvictsOldestBeyondCapacity(t *testing.T) {
	c := newVerdictCache(2)
	c.put("a", cachedVerdict{binary: true})
	c.put("b", cachedVerdict{binary: true, dangerous: true})
	c.put("c", cachedVerdict{binary: true})

	if _, ok := c.get("a"); ok {
		t.Fatal("oldest entry should have been evicted")
	}
	if _, ok := c.get("b"); !ok {
		t.Fatal("entry b should remain")
	}
	if _, ok := c.get("c"); !ok {
		t.Fatal("entry c should remain")
	}
}
