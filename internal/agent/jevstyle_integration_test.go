package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/jevstyle"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

type stubJevDecider struct {
	dangerousResult    bool
	dangerousErr       error
	disambiguateResult string
	disambiguateOk     bool
	disambiguateErr    error
	goalDoneResult     bool
	goalDoneErr        error
	failureClassResult string
	failureClassErr    error
	commitClassResult  string
	commitClassErr     error
	enabled            bool
	model              string
}

func (s *stubJevDecider) Decide(ctx context.Context, req jevstyle.DecisionRequest) (*jevstyle.DecisionResponse, error) {
	return nil, nil
}
func (s *stubJevDecider) DecideBool(ctx context.Context, state, question string) (bool, error) {
	return false, nil
}
func (s *stubJevDecider) DecideChoice(ctx context.Context, state, question string, options ...string) (string, int, error) {
	return "", -1, nil
}
func (s *stubJevDecider) IsCommandDangerous(ctx context.Context, command string) (bool, error) {
	return s.dangerousResult, s.dangerousErr
}
func (s *stubJevDecider) DisambiguatePath(ctx context.Context, hint string, candidates []string) (string, bool, error) {
	return s.disambiguateResult, s.disambiguateOk, s.disambiguateErr
}
func (s *stubJevDecider) CheckGoalCompletion(ctx context.Context, goal, recentProgress string) (bool, error) {
	return s.goalDoneResult, s.goalDoneErr
}
func (s *stubJevDecider) ClassifyFailure(ctx context.Context, errorLog string) (string, error) {
	return s.failureClassResult, s.failureClassErr
}
func (s *stubJevDecider) ClassifyCommit(ctx context.Context, diffSummary string) (string, error) {
	return s.commitClassResult, s.commitClassErr
}
func (s *stubJevDecider) Enabled() bool {
	return s.enabled
}
func (s *stubJevDecider) Model() string {
	return s.model
}

func TestAgentJevstyleWiring(t *testing.T) {
	cfg := &config.Config{
		Cwd:           t.TempDir(),
		Model:         "main-model",
		JevstyleModel: "jev-model",
	}
	ag := New(cfg, nil, tools.NewRegistry(), permissions.NewManager(cfg), session.New(cfg), &fakeUI{})

	if ag.Jevstyle() == nil {
		t.Fatal("expected Jevstyle decider to be initialized when JevstyleModel is configured")
	}

	stub := &stubJevDecider{enabled: true, model: "custom-jev"}
	ag.SetJevstyle(stub)
	if ag.Jevstyle() != stub {
		t.Fatal("SetJevstyle did not store the decider")
	}
	if ag.reg.Get("JevDecide") == nil {
		t.Fatal("expected JevDecide tool to be registered in agent registry")
	}
}

func TestAgentJevstyleDisambiguation(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:           tmp,
		Model:         "main-model",
		JevstyleModel: "jev-model",
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

	stub := &stubJevDecider{
		enabled:            true,
		model:              "jev-model",
		disambiguateResult: p2,
		disambiguateOk:     true,
	}
	ag.SetJevstyle(stub)

	params := map[string]any{"file_path": "flags.go"}
	ag.rescuePathParam(context.Background(), "Read", params)

	if params["file_path"] != p2 {
		t.Fatalf("expected jevstyle to disambiguate to %q, got %q", p2, params["file_path"])
	}
}

func TestAgentJevstyleFailureDiagnosis(t *testing.T) {
	cfg := &config.Config{
		Cwd:           t.TempDir(),
		Model:         "main-model",
		JevstyleModel: "jev-model",
		YesMode:       true,
	}
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	ag := New(cfg, nil, reg, permissions.NewManager(cfg), session.New(cfg), &fakeUI{})

	stub := &stubJevDecider{
		enabled:            true,
		model:              "jev-model",
		failureClassResult: "Missing package, module, or dependency",
	}
	ag.SetJevstyle(stub)

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
		JevstyleModel: "jev-model",
	}
	ag := New(cfg, nil, tools.NewRegistry(), permissions.NewManager(cfg), session.New(cfg), &fakeUI{})

	// No mission active -> false
	done, err := ag.CheckMissionCompletion(context.Background())
	if err != nil || done {
		t.Fatalf("expected false when no mission active, got done=%t err=%v", done, err)
	}

	// Start mission
	ag.mission.Start("Build the authentication system")

	stub := &stubJevDecider{
		enabled:        true,
		model:          "jev-model",
		goalDoneResult: true,
	}
	ag.SetJevstyle(stub)

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
