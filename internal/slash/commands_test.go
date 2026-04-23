package slash

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

type fakePlanAgent struct {
	plan bool
}

func (f *fakePlanAgent) EnterPlanMode() { f.plan = true }
func (f *fakePlanAgent) ExitPlanMode()  { f.plan = false }
func (f *fakePlanAgent) InPlanMode() bool {
	return f.plan
}

func TestDispatchMinimumCommands(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   tmp,
	}
	s := session.New(cfg)

	var out bytes.Buffer
	planAgent := &fakePlanAgent{}
	ctx := &Ctx{
		Cfg:     cfg,
		Session: s,
		Agent:   planAgent,
		Out:     &out,
	}

	handled, shouldExit, err := Dispatch(ctx, "/yes")
	if err != nil || !handled || shouldExit || !cfg.YesMode {
		t.Fatalf("unexpected /yes result: handled=%t exit=%t err=%v yes=%t", handled, shouldExit, err, cfg.YesMode)
	}

	handled, shouldExit, err = Dispatch(ctx, "/no")
	if err != nil || !handled || shouldExit || cfg.YesMode {
		t.Fatalf("unexpected /no result: handled=%t exit=%t err=%v yes=%t", handled, shouldExit, err, cfg.YesMode)
	}

	s.AddUser("hello")
	handled, shouldExit, err = Dispatch(ctx, "/status")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /status result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "Model:") {
		t.Fatalf("expected status output, got %q", out.String())
	}

	handled, shouldExit, err = Dispatch(ctx, "/plan")
	if err != nil || !handled || shouldExit || !planAgent.InPlanMode() {
		t.Fatalf("unexpected /plan result: handled=%t exit=%t err=%v plan=%t", handled, shouldExit, err, planAgent.InPlanMode())
	}

	handled, shouldExit, err = Dispatch(ctx, "/approve")
	if err != nil || !handled || shouldExit || planAgent.InPlanMode() {
		t.Fatalf("unexpected /approve result: handled=%t exit=%t err=%v plan=%t", handled, shouldExit, err, planAgent.InPlanMode())
	}

	handled, shouldExit, err = Dispatch(ctx, "/model")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /model inspect result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}

	handled, shouldExit, err = Dispatch(ctx, "/model qwen3.5:9b")
	if err != nil || !handled || shouldExit || cfg.Model != "qwen3.5:9b" {
		t.Fatalf("unexpected /model set result: handled=%t exit=%t err=%v model=%s", handled, shouldExit, err, cfg.Model)
	}

	handled, shouldExit, err = Dispatch(ctx, "/tokens")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /tokens result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}

	for i := 0; i < 40; i++ {
		s.AddUser("x")
	}
	handled, shouldExit, err = Dispatch(ctx, "/compact")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /compact result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}

	handled, shouldExit, err = Dispatch(ctx, "/commit")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /commit result outside repo: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
}

func TestSessionsAndResumeCommands(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}

	// Pre-create a saved session for the same project so /resume (no args)
	// finds it via project-index.json.
	prev := session.New(cfg)
	prev.AddUser("look for me on resume")
	prev.AddAssistant("ok")
	if err := prev.Save(); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	prevID := prev.ID()

	// Live session starts empty; /resume should not save the empty
	// transcript and should swap to prevID.
	live := session.New(cfg)
	var out bytes.Buffer
	ctx := &Ctx{
		Cfg:     cfg,
		Session: live,
		Agent:   &fakePlanAgent{},
		Out:     &out,
	}

	handled, shouldExit, err := Dispatch(ctx, "/sessions")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /sessions result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), prevID) {
		t.Fatalf("expected /sessions output to mention seeded id %s, got %q", prevID, out.String())
	}
	if !strings.Contains(out.String(), "*") {
		t.Fatalf("expected current-project marker (*) in /sessions output, got %q", out.String())
	}

	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/resume")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /resume result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if live.ID() != prevID {
		t.Fatalf("expected /resume to swap to %s, got %s", prevID, live.ID())
	}
	if live.MessageCount() != 2 {
		t.Fatalf("expected 2 loaded messages after /resume, got %d", live.MessageCount())
	}

	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/resume "+prevID)
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /resume <id> result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if live.ID() != prevID {
		t.Fatalf("/resume <id> should keep id %s, got %s", prevID, live.ID())
	}
}
