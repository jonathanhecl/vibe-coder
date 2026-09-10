package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

type thinkRecordingClient struct {
	lastThink *ollama.ThinkSetting
	calls     int32
}

func (c *thinkRecordingClient) Chat(_ context.Context, req ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	atomic.AddInt32(&c.calls, 1)
	if req.Think == nil {
		c.lastThink = nil
	} else {
		cp := *req.Think
		c.lastThink = &cp
	}
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Delta: "done", Done: true}
	close(ch)
	return ch, nil
}
func (c *thinkRecordingClient) ChatSync(context.Context, ollama.ChatRequest) (ollama.ChatResponse, error) {
	return ollama.ChatResponse{}, errors.New("not used")
}
func (c *thinkRecordingClient) Tags(context.Context) ([]ollama.Model, error) {
	return nil, errors.New("not used")
}
func (c *thinkRecordingClient) Version(context.Context) (string, error) {
	return "", errors.New("not used")
}
func (c *thinkRecordingClient) Pull(context.Context, string, func(ollama.PullEvent)) error {
	return errors.New("not used")
}

func runOneTurn(t *testing.T, cfg *config.Config) *thinkRecordingClient {
	t.Helper()
	rc := &thinkRecordingClient{}
	reg := tools.NewRegistry()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	ag := New(cfg, rc, reg, perm, session.New(cfg), &fakeUI{})
	if err := ag.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if atomic.LoadInt32(&rc.calls) == 0 {
		t.Fatal("expected at least one chat call")
	}
	return rc
}

func thinkWireValue(t *ollama.ThinkSetting) string {
	if t == nil {
		return "unset"
	}
	if t.Level != "" {
		return t.Level
	}
	if t.Enabled {
		return "true"
	}
	return "false"
}

func TestAgentSendsThinkLevel(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	base := &config.Config{Model: "m", ContextWindow: 8000, MaxTokens: 32, Cwd: tmp, SessionsDir: tmp}

	withLevel := *base
	withLevel.OllamaThinkLevel = "low"
	if got := thinkWireValue(runOneTurn(t, &withLevel).lastThink); got != "low" {
		t.Fatalf("expected think:low on the wire, got %s", got)
	}

	off := *base
	off.OllamaThinkLevel = "off"
	if got := thinkWireValue(runOneTurn(t, &off).lastThink); got != "false" {
		t.Fatalf("expected explicit think:false on the wire, got %s", got)
	}

	def := *base
	if got := thinkWireValue(runOneTurn(t, &def).lastThink); got != "true" {
		t.Fatalf("expected default think:true preserved, got %s", got)
	}

	legacy := *base
	legacy.OllamaNoThink = true
	if got := thinkWireValue(runOneTurn(t, &legacy).lastThink); got != "false" {
		t.Fatalf("expected legacy no-think to send false, got %s", got)
	}

	knownPlain := *base
	knownPlain.ThinkingKnown = true
	knownPlain.ThinkingSupported = false
	if got := thinkWireValue(runOneTurn(t, &knownPlain).lastThink); got != "unset" {
		t.Fatalf("expected omitted think for known non-thinker, got %s", got)
	}
}
