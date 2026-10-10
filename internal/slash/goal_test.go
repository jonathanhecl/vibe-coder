package slash

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func TestGoalCommandWithoutArgsShowsUsage(t *testing.T) {
	ctx, out, _ := newMissionTestCtx(tools.Mission{})

	if _, _, err := Dispatch(ctx, "/goal"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Usage: /goal <objective>") {
		t.Fatalf("expected usage text, got %q", out.String())
	}
}

func TestGoalCommandWithoutArgsShowsActiveMission(t *testing.T) {
	ctx, out, _ := newMissionTestCtx(tools.Mission{
		Goal:   "build new feature",
		Status: tools.MissionStatusActive,
		Turns:  2,
	})

	if _, _, err := Dispatch(ctx, "/goal"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Goal: build new feature") {
		t.Fatalf("expected active mission goal, got %q", out.String())
	}
}

func TestGoalCommandStartsMission(t *testing.T) {
	ctx, out, ag := newMissionTestCtx(tools.Mission{})

	if _, _, err := Dispatch(ctx, "/goal Refactor database queries and add tests"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Autonomous mission initialized") {
		t.Fatalf("expected mission initialized notice, got %q", out.String())
	}
	snap := ag.MissionSnapshot()
	if snap.Status != tools.MissionStatusActive {
		t.Fatalf("expected status active, got %q", snap.Status)
	}
	if snap.Goal != "Refactor database queries and add tests" {
		t.Fatalf("expected goal recorded, got %q", snap.Goal)
	}
}

func TestGoalCommandWhenAlreadyActiveWarns(t *testing.T) {
	ctx, out, _ := newMissionTestCtx(tools.Mission{
		Goal:   "ongoing task",
		Status: tools.MissionStatusActive,
	})

	if _, _, err := Dispatch(ctx, "/goal Another task"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "A mission is already active") {
		t.Fatalf("expected already active warning, got %q", out.String())
	}
}

func TestMissionStartSubcommandStartsMission(t *testing.T) {
	ctx, out, ag := newMissionTestCtx(tools.Mission{})

	if _, _, err := Dispatch(ctx, "/mission start Upgrade Go version"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Autonomous mission initialized") {
		t.Fatalf("expected mission initialized notice, got %q", out.String())
	}
	snap := ag.MissionSnapshot()
	if snap.Status != tools.MissionStatusActive || snap.Goal != "Upgrade Go version" {
		t.Fatalf("expected active mission with goal, got %#v", snap)
	}
}

func TestGoalTaskFromSlash(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/goal", "", false},
		{"/goal ", "", false},
		{"/goals foo", "", false},
		{"/goal Fix the bug", "Fix the bug", true},
		{"/goal    Refactor auth module   ", "Refactor auth module", true},
		{"/mission start", "", false},
		{"/mission start Deploy app", "Deploy app", true},
		{"/plan something", "", false},
		{"regular message", "", false},
	}

	for _, tc := range cases {
		got, ok := GoalTaskFromSlash(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("GoalTaskFromSlash(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFormatGoalPrompt(t *testing.T) {
	prompt := FormatGoalPrompt("Implement caching layer")
	if !strings.Contains(prompt, "Autonomous goal: \"Implement caching layer\"") {
		t.Fatalf("prompt missing goal title: %s", prompt)
	}
	if !strings.Contains(prompt, "Phase 1: Planning, Verification Design & Clarification") {
		t.Fatalf("prompt missing phase 1: %s", prompt)
	}
	if !strings.Contains(prompt, "TodoWrite") {
		t.Fatalf("prompt missing TodoWrite instruction: %s", prompt)
	}
	if !strings.Contains(prompt, "Phase 2: Autonomous Execution & Verification") {
		t.Fatalf("prompt missing phase 2: %s", prompt)
	}
	if !strings.Contains(prompt, "MissionComplete") || !strings.Contains(prompt, "MissionBlocked") {
		t.Fatalf("prompt missing completion tools: %s", prompt)
	}
}

func TestGoalCommandWhenNoMissionController(t *testing.T) {
	tmp := "."
	cfg := &config.Config{Model: "m", ContextWindow: 32768, Cwd: tmp, SessionsDir: tmp}
	out := &bytes.Buffer{}
	ctx := &Ctx{Cfg: cfg, Session: session.New(cfg), Agent: &fakePlanAgent{}, Out: out}

	if _, _, err := Dispatch(ctx, "/goal something"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "unavailable") {
		t.Fatalf("expected unavailable notice, got %q", out.String())
	}
}
