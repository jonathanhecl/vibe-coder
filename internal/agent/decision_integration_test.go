package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/decision"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

type stubDecider struct {
	dangerousResult     bool
	dangerousErr        error
	networkAccessResult bool
	networkAccessErr    error
	disambiguateResult  string
	disambiguateOk      bool
	disambiguateErr     error
	goalDoneResult      bool
	goalDoneErr         error
	failureClassResult  string
	failureClassErr     error
	commitClassResult   string
	commitClassErr      error
	thinkingLoopResult  bool
	thinkingLoopErr     error
	enabled             bool
	model               string
}

func (s *stubDecider) Decide(ctx context.Context, req decision.Request) (*decision.Response, error) {
	return nil, nil
}
func (s *stubDecider) DecideBool(ctx context.Context, state, question string) (bool, error) {
	return false, nil
}
func (s *stubDecider) DecideChoice(ctx context.Context, state, question string, options ...string) (string, int, error) {
	return "", -1, nil
}
func (s *stubDecider) IsCommandDangerous(ctx context.Context, command string) (bool, error) {
	return s.dangerousResult, s.dangerousErr
}
func (s *stubDecider) IsCommandNetworkAccess(ctx context.Context, command string) (bool, error) {
	return s.networkAccessResult, s.networkAccessErr
}
func (s *stubDecider) DisambiguatePath(ctx context.Context, hint string, candidates []string) (string, bool, error) {
	return s.disambiguateResult, s.disambiguateOk, s.disambiguateErr
}
func (s *stubDecider) CheckGoalCompletion(ctx context.Context, goal, recentProgress string) (bool, error) {
	return s.goalDoneResult, s.goalDoneErr
}
func (s *stubDecider) ClassifyFailure(ctx context.Context, errorLog string) (string, error) {
	return s.failureClassResult, s.failureClassErr
}
func (s *stubDecider) ClassifyCommit(ctx context.Context, diffSummary string) (string, error) {
	return s.commitClassResult, s.commitClassErr
}
func (s *stubDecider) CheckThinkingLoop(ctx context.Context, thinkingText string) (bool, error) {
	return s.thinkingLoopResult, s.thinkingLoopErr
}
func (s *stubDecider) Enabled() bool {
	return s.enabled
}
func (s *stubDecider) Model() string {
	return s.model
}

func TestAgentDecisionWiring(t *testing.T) {
	cfg := &config.Config{
		Cwd:           t.TempDir(),
		Model:         "main-model",
		DecisionModel: "decision-model",
	}
	ag := New(cfg, nil, tools.NewRegistry(), permissions.NewManager(cfg), session.New(cfg), &fakeUI{})

	if ag.Decider() == nil {
		t.Fatal("expected decision decider to be initialized when DecisionModel is configured")
	}

	stub := &stubDecider{enabled: true, model: "custom-decision"}
	ag.SetDecider(stub)
	if ag.Decider() != stub {
		t.Fatal("SetDecider did not store the decider")
	}
	if ag.reg.Get("Decide") == nil {
		t.Fatal("expected Decide tool to be registered in agent registry")
	}
}

func TestAgentDecisionDisambiguation(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:           tmp,
		Model:         "main-model",
		DecisionModel: "decision-model",
	}
	ag := New(cfg, nil, tools.NewRegistry(), permissions.NewManager(cfg), session.New(cfg), &fakeUI{})

	// Populate path memory with two candidate files sharing basename "flags.go"
	p1 := filepath.Join(tmp, "pkg", "flags.go")
	p2 := filepath.Join(tmp, "internal", "config", "flags.go")
	_ = os.MkdirAll(filepath.Dir(p1), 0o755)
	_ = os.MkdirAll(filepath.Dir(p2), 0o755)
	_ = os.WriteFile(p1, []byte("pkg"), 0o644)
	_ = os.WriteFile(p2, []byte("cfg"), 0o644)

	ag.paths.RememberToolResult("Write", map[string]any{"file_path": p1}, "ok", false)
	ag.paths.RememberToolResult("Write", map[string]any{"file_path": p2}, "ok", false)

	stub := &stubDecider{
		enabled:            true,
		model:              "decision-model",
		disambiguateResult: p2,
		disambiguateOk:     true,
	}
	ag.SetDecider(stub)

	params := map[string]any{"file_path": "flags.go"}
	ag.rescuePathParam(context.Background(), "Read", params)

	if params["file_path"] != p2 {
		t.Fatalf("expected decision model to disambiguate to %q, got %q", p2, params["file_path"])
	}
}

func TestAgentDecisionFailureDiagnosis(t *testing.T) {
	cfg := &config.Config{
		Cwd:           t.TempDir(),
		Model:         "main-model",
		DecisionModel: "decision-model",
		YesMode:       true,
	}
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	ag := New(cfg, nil, reg, permissions.NewManager(cfg), session.New(cfg), &fakeUI{})

	stub := &stubDecider{
		enabled:            true,
		model:              "decision-model",
		failureClassResult: "Missing package, module, or dependency",
	}
	ag.SetDecider(stub)

	// Execute a bash command that fails
	bashTool := reg.Get("Bash")
	if bashTool == nil {
		t.Fatal("missing Bash tool")
	}
	res, _, err := ag.executeTool(context.Background(), bashTool, "Bash", map[string]any{"command": "nonexistent_command_12345"}, toolExecutionMode{})
	if err != nil {
		t.Fatalf("unexpected execution error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected nonexistent command to fail")
	}
	if !strings.Contains(res.HintsForModel, "[Failure Diagnosis: Missing package, module, or dependency]") {
		t.Fatalf("expected failure diagnosis hint, got: %q", res.HintsForModel)
	}
}

func TestAgentCheckMissionCompletion(t *testing.T) {
	cfg := &config.Config{
		Cwd:           t.TempDir(),
		Model:         "main-model",
		DecisionModel: "decision-model",
	}
	ag := New(cfg, nil, tools.NewRegistry(), permissions.NewManager(cfg), session.New(cfg), &fakeUI{})

	// No mission active -> false
	done, err := ag.CheckMissionCompletion(context.Background())
	if err != nil || done {
		t.Fatalf("expected false when no mission active, got done=%t err=%v", done, err)
	}

	// Start mission
	ag.mission.Start("Build the authentication system")

	stub := &stubDecider{
		enabled:        true,
		model:          "decision-model",
		goalDoneResult: true,
	}
	ag.SetDecider(stub)

	done, err = ag.CheckMissionCompletion(context.Background())
	if err != nil || !done {
		t.Fatalf("expected mission completion true, got done=%t err=%v", done, err)
	}

	// Test CompleteMission
	ag.CompleteMission("All done")
	if ag.MissionActive() {
		t.Fatal("expected mission to be inactive after CompleteMission")
	}
}
