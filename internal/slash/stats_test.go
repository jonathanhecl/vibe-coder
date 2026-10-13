package slash

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/metrics"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

type fakeStatsAgent struct {
	fakePlanAgent
	summary metrics.Summary
	path    string
}

func (f *fakeStatsAgent) MetricsSummary() metrics.Summary { return f.summary }
func (f *fakeStatsAgent) MetricsPath() string             { return f.path }

func TestStatsCommandPrintsSummaryAndPath(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{Model: "m", ContextWindow: 32768, Cwd: tmp, SessionsDir: tmp}
	s := session.New(cfg)

	summary := metrics.Summary{Requests: 2, PromptTokens: 400, CompletionTokens: 40, WallMS: 600}
	out := &bytes.Buffer{}
	ctx := &Ctx{Cfg: cfg, Session: s, Agent: &fakeStatsAgent{summary: summary, path: "/tmp/m.jsonl"}, Out: out}

	handled, shouldExit, err := Dispatch(ctx, "/stats")
	if err != nil || !handled || shouldExit {
		t.Fatalf("/stats: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	text := out.String()
	for _, want := range []string{"Requests: 2", "Prompt tokens: 400", "Metrics file: /tmp/m.jsonl"} {
		if !strings.Contains(text, want) {
			t.Fatalf("/stats missing %q:\n%s", want, text)
		}
	}
}

func TestStatsCommandDisabledFile(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{Model: "m", ContextWindow: 32768, Cwd: tmp, SessionsDir: tmp}
	s := session.New(cfg)

	out := &bytes.Buffer{}
	ctx := &Ctx{Cfg: cfg, Session: s, Agent: &fakeStatsAgent{}, Out: out}
	if _, _, err := Dispatch(ctx, "/stats"); err != nil {
		t.Fatalf("/stats: %v", err)
	}
	if !strings.Contains(out.String(), "Metrics file disabled") {
		t.Fatalf("expected disabled note, got %q", out.String())
	}
}

func TestStatsCommandWithoutReporter(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{Model: "m", ContextWindow: 32768, Cwd: tmp, SessionsDir: tmp}
	s := session.New(cfg)

	out := &bytes.Buffer{}
	ctx := &Ctx{Cfg: cfg, Session: s, Agent: &fakePlanAgent{}, Out: out}
	if _, _, err := Dispatch(ctx, "/stats"); err != nil {
		t.Fatalf("/stats: %v", err)
	}
	if !strings.Contains(out.String(), "No metrics") {
		t.Fatalf("expected no-metrics note, got %q", out.String())
	}
}
