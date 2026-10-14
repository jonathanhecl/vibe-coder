package slash

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/metrics"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
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
	if !strings.Contains(out.String(), "Metrics file: disabled") {
		t.Fatalf("expected disabled note, got %q", out.String())
	}
}

// TestStatsStyledUsesSharedBlockLayout pins the design /stats shares with
// /status: the command heading, the aligned label column, and the colored notes.
func TestStatsStyledUsesSharedBlockLayout(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{Model: "m", ContextWindow: 32768, Cwd: tmp, SessionsDir: tmp}
	s := session.New(cfg)

	summary := metrics.Summary{Requests: 2, Errors: 1, PromptTokens: 400, CompletionTokens: 40, WallMS: 600}
	out := &bytes.Buffer{}
	ctx := &Ctx{
		Cfg:     cfg,
		Session: s,
		Agent:   &fakeStatsAgent{summary: summary, path: "/tmp/m.jsonl"},
		Out:     out,
	}
	writeStats(ctx, tui.NewStyleForTest(true))

	rendered := out.String()
	for _, want := range []string{
		"\x1b[1m\x1b[92mvibe\x1b[0m \x1b[90mstats\x1b[0m",
		"\x1b[1m\x1b[36mRequests",
		"\x1b[97m2\x1b[0m",
		"\x1b[31m(1 errors)\x1b[0m",
		"\x1b[2m (avg 200/request)\x1b[0m",
		"\x1b[2m (avg 300 ms/request)\x1b[0m",
		"\x1b[90m/tmp/m.jsonl\x1b[0m",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("/stats styled output missing %q:\n%q", want, rendered)
		}
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
