package slash

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/agent"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// The real agent runtime must satisfy the interface /mission asserts on, so a
// refactor that renames these methods fails the build instead of silently
// disabling the command.
var _ missionController = (*agent.Agent)(nil)

// fakeMissionAgent adds mission control to the plan-mode fake so /mission can
// be exercised without the real agent runtime.
type fakeMissionAgent struct {
	fakePlanAgent
	mission tools.Mission
}

func (f *fakeMissionAgent) MissionSnapshot() tools.Mission { return f.mission }
func (f *fakeMissionAgent) CompleteMission(summary string) {
	f.mission.Status = tools.MissionStatusCompleted
	f.mission.Summary = summary
}
func (f *fakeMissionAgent) BlockActiveMission(reason string) {
	f.mission.Status = tools.MissionStatusBlocked
	f.mission.Summary = reason
}

func newMissionTestCtx(m tools.Mission) (*Ctx, *bytes.Buffer, *fakeMissionAgent) {
	tmp := "."
	cfg := &config.Config{Model: "m", ContextWindow: 32768, Cwd: tmp, SessionsDir: tmp}
	ag := &fakeMissionAgent{mission: m}
	out := &bytes.Buffer{}
	return &Ctx{Cfg: cfg, Session: session.New(cfg), Agent: ag, Out: out}, out, ag
}

func TestMissionStatusWithoutMission(t *testing.T) {
	ctx, out, _ := newMissionTestCtx(tools.Mission{})

	if _, _, err := Dispatch(ctx, "/mission"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No mission") {
		t.Fatalf("expected a no-mission notice, got %q", out.String())
	}
}

func TestMissionStatusShowsActiveMission(t *testing.T) {
	ctx, out, _ := newMissionTestCtx(tools.Mission{
		Goal:   "refactor the script",
		Status: tools.MissionStatusActive,
		Turns:  3,
	})

	if _, _, err := Dispatch(ctx, "/mission status"); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{"Mission: active", "Goal: refactor the script", "Turns: 3", "/mission done"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in mission status, got:\n%s", want, rendered)
		}
	}
}

func TestMissionDoneCompletesByUser(t *testing.T) {
	ctx, out, ag := newMissionTestCtx(tools.Mission{Goal: "g", Status: tools.MissionStatusActive})

	if _, _, err := Dispatch(ctx, "/mission done lo hice yo"); err != nil {
		t.Fatal(err)
	}
	if ag.mission.Status != tools.MissionStatusCompleted {
		t.Fatalf("expected mission completed, got %q", ag.mission.Status)
	}
	if ag.mission.Summary != "lo hice yo" {
		t.Fatalf("expected the user summary to be kept, got %q", ag.mission.Summary)
	}
	if !strings.Contains(out.String(), "Mission completed") {
		t.Fatalf("expected completion confirmation, got %q", out.String())
	}
	if ctx.Session.MessageCount() == 0 {
		t.Fatal("expected a system note so the model learns the mission was closed")
	}
}

func TestMissionCancelStopsWithoutCompleting(t *testing.T) {
	ctx, out, ag := newMissionTestCtx(tools.Mission{Goal: "g", Status: tools.MissionStatusActive})

	if _, _, err := Dispatch(ctx, "/mission cancel user did it"); err != nil {
		t.Fatal(err)
	}
	if ag.mission.Status != tools.MissionStatusBlocked {
		t.Fatalf("expected mission blocked/cancelled, got %q", ag.mission.Status)
	}
	if ag.mission.Summary != "user did it" {
		t.Fatalf("expected the reason to be kept, got %q", ag.mission.Summary)
	}
	if !strings.Contains(out.String(), "Mission cancelled") {
		t.Fatalf("expected cancellation confirmation, got %q", out.String())
	}
}

func TestMissionDoneWithoutActiveMission(t *testing.T) {
	ctx, out, ag := newMissionTestCtx(tools.Mission{Status: tools.MissionStatusCompleted})

	if _, _, err := Dispatch(ctx, "/mission done"); err != nil {
		t.Fatal(err)
	}
	if ag.mission.Status != tools.MissionStatusCompleted {
		t.Fatalf("expected the mission to stay completed, got %q", ag.mission.Status)
	}
	if !strings.Contains(out.String(), "No active mission") {
		t.Fatalf("expected a no-active-mission notice, got %q", out.String())
	}
}

func TestMissionUsageOnUnknownSubcommand(t *testing.T) {
	ctx, out, _ := newMissionTestCtx(tools.Mission{Status: tools.MissionStatusActive})

	if _, _, err := Dispatch(ctx, "/mission bogus"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Usage: /mission") {
		t.Fatalf("expected usage text, got %q", out.String())
	}
}

func TestMissionUnavailableWithoutController(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{Model: "m", ContextWindow: 32768, Cwd: tmp, SessionsDir: tmp}
	out := &bytes.Buffer{}
	ctx := &Ctx{Cfg: cfg, Session: session.New(cfg), Agent: &fakePlanAgent{}, Out: out}

	if _, _, err := Dispatch(ctx, "/mission"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "unavailable") {
		t.Fatalf("expected an unavailable notice, got %q", out.String())
	}
}
