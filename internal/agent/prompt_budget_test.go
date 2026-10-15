package agent

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/prompt"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func newPromptBudgetAgent(t *testing.T, toolsKnown, toolsSupported bool) *Agent {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:          "m",
		ContextWindow:  8000,
		Cwd:            tmp,
		SessionsDir:    tmp,
		ToolsKnown:     toolsKnown,
		ToolsSupported: toolsSupported,
	}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	ag := New(cfg, fakeClient{}, reg, perm, sess, &fakeUI{})
	ag.currentGoal = "fix the failing test"
	return ag
}

func TestSystemPromptNativeModeHasNoXMLProtocol(t *testing.T) {
	t.Parallel()
	ag := newPromptBudgetAgent(t, true, true)
	p := ag.buildSystemPrompt()
	if strings.Contains(p, `<invoke name=`) {
		t.Fatalf("native-mode system prompt must not carry the XML protocol:\n%s", p)
	}
	if !strings.Contains(p, "# Tool use") {
		t.Fatal("native-mode system prompt should keep the tool guidelines")
	}
}

func TestSystemPromptXMLModeListsTools(t *testing.T) {
	t.Parallel()
	ag := newPromptBudgetAgent(t, true, false)
	p := ag.buildSystemPrompt()
	if !strings.Contains(p, `<invoke name="ToolName">`) {
		t.Fatalf("xml-mode system prompt must teach the invoke envelope:\n%s", p)
	}
	if !strings.Contains(p, "Read(") {
		t.Fatalf("xml-mode system prompt must list the tool catalogue:\n%s", p)
	}
}

func TestSystemPromptXMLModeWhenToolsUnknown(t *testing.T) {
	t.Parallel()
	ag := newPromptBudgetAgent(t, false, false)
	if !ag.xmlToolMode() {
		t.Fatal("unknown tool support must fall back to XML mode")
	}
	p := ag.buildSystemPrompt()
	if !strings.Contains(p, `<invoke name="ToolName">`) {
		t.Fatal("unknown tool support should render the XML protocol")
	}
}

func TestSystemPromptOfflineDropsInternetClaim(t *testing.T) {
	t.Parallel()
	ag := newPromptBudgetAgent(t, true, true)
	ag.perm.SetOfflineMode(true)
	p := ag.buildSystemPrompt()
	if !strings.Contains(p, "# Offline Mode") {
		t.Fatalf("offline system prompt must include the offline block:\n%s", p)
	}
	if strings.Contains(p, "internet access") {
		t.Fatalf("offline system prompt must not claim internet access:\n%s", p)
	}
}

func TestGoalBlockHasNoInvokeSyntax(t *testing.T) {
	t.Parallel()
	out := prompt.Render("goal", struct{ Goal string }{Goal: "fix the failing test"})
	if strings.Contains(out, "<invoke") {
		t.Fatalf("goal block must not contain invoke syntax:\n%s", out)
	}
	if !strings.Contains(out, "fix the failing test") {
		t.Fatalf("goal block must carry the user goal:\n%s", out)
	}
}

func TestSystemPromptStableAcrossGoalChanges(t *testing.T) {
	t.Parallel()
	ag := newPromptBudgetAgent(t, true, true)
	ag.currentGoal = "first user request"
	p1 := ag.buildSystemPrompt()
	ag.currentGoal = "second user request"
	p2 := ag.buildSystemPrompt()
	if p1 != p2 {
		t.Fatal("system prompt must stay byte-identical across user goal changes to preserve prefix KV cache")
	}
	if strings.Contains(p1, "<<<USER_GOAL>>>") {
		t.Fatal("system prompt must not embed the per-turn goal")
	}
}

func TestGoalAnchoredInRuntimeStateNoteOnMultiStepTurn(t *testing.T) {
	t.Parallel()
	ag := newPromptBudgetAgent(t, true, true)
	ag.currentGoal = "fix the failing test"

	// Single-shot turn before any tool call: no duplicate goal reminder.
	if note := ag.runtimeStateNote(); note != "" {
		t.Fatalf("expected empty runtimeStateNote on fresh single-shot turn, got %q", note)
	}
	// Continuation prompt ("continua"): goal anchor appears immediately so the
	// resolved earlier ask is visible even before the first tool call.
	ag.sess.AddUser("continua")
	if note := ag.runtimeStateNote(); !strings.Contains(note, "fix the failing test") {
		t.Fatalf("expected goal anchor on continuation prompt, got %q", note)
	}
	ag.sess.Clear()
	// Multi-step turn (after a tool call executes): goal is anchored at the tail.
	ag.noteToolExecuted()
	note := ag.runtimeStateNote()
	if !strings.Contains(note, "<<<USER_GOAL>>>") || !strings.Contains(note, "fix the failing test") {
		t.Fatalf("expected goal anchor in runtimeStateNote after tool execution, got %q", note)
	}
}

// nativeSystemPromptTokenCeiling guards the prompt-size win from regressing.
// Measured after the Phase-A overhaul at 1529 estimated tokens; the ceiling
// is that value +10% — raise deliberately, never as a side effect.
const nativeSystemPromptTokenCeiling = 1682

func TestNativeSystemPromptWithinTokenBudget(t *testing.T) {
	t.Parallel()
	ag := newPromptBudgetAgent(t, true, true)
	est := session.EstimateTokens(ag.buildSystemPrompt())
	if est > nativeSystemPromptTokenCeiling {
		t.Fatalf("native system prompt estimate %d exceeds ceiling %d", est, nativeSystemPromptTokenCeiling)
	}
}
