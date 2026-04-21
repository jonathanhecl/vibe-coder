package slash

import (
	"bytes"
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
