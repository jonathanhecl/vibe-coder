package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/sidecar"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

type fallbackMainClient struct {
	calls int
	err   error
}

func (f *fallbackMainClient) Chat(_ context.Context, _ ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	f.calls++
	return nil, f.err
}
func (f *fallbackMainClient) ChatSync(_ context.Context, _ ollama.ChatRequest) (ollama.ChatResponse, error) {
	return ollama.ChatResponse{}, nil
}
func (f *fallbackMainClient) Tags(_ context.Context) ([]ollama.Model, error) { return nil, nil }
func (f *fallbackMainClient) Version(_ context.Context) (string, error)      { return "", nil }
func (f *fallbackMainClient) Pull(_ context.Context, _ string, _ func(ollama.PullEvent)) error {
	return nil
}

type fallbackSidecarClient struct {
	calls int
	reply string
	err   error
}

func (s *fallbackSidecarClient) Chat(_ context.Context, _ ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	ch := make(chan ollama.Chunk, 2)
	ch <- ollama.Chunk{Delta: s.reply, Done: true}
	close(ch)
	return ch, nil
}
func (s *fallbackSidecarClient) ChatSync(_ context.Context, _ ollama.ChatRequest) (ollama.ChatResponse, error) {
	return ollama.ChatResponse{Content: s.reply}, nil
}
func (s *fallbackSidecarClient) Tags(_ context.Context) ([]ollama.Model, error) { return nil, nil }
func (s *fallbackSidecarClient) Version(_ context.Context) (string, error)      { return "", nil }
func (s *fallbackSidecarClient) Pull(_ context.Context, _ string, _ func(ollama.PullEvent)) error {
	return nil
}

func newFallbackAgent(t *testing.T, mainClient, sideClient ollama.Client) *Agent {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "main-model",
		SidecarModel:  "sidecar-model",
		ContextWindow: 4096,
		MaxTokens:     128,
		Temperature:   0.2,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{})
	sess := session.New(cfg)
	ag := New(cfg, mainClient, reg, perm, sess, &coverageUI{})
	if sideClient != nil {
		ag.SetSidecar(sidecar.New(cfg, sideClient))
	}
	return ag
}

func TestSidecarFallbackOnConnectionError(t *testing.T) {
	main := &fallbackMainClient{err: errors.New("dial tcp 127.0.0.1:11434: connect: connection refused")}
	side := &fallbackSidecarClient{reply: "sidecar reply"}
	ag := newFallbackAgent(t, main, side)

	reply, _, err := ag.chatOnce(context.Background())
	if err != nil {
		t.Fatalf("expected sidecar fallback to succeed, got error: %v", err)
	}
	if reply != "sidecar reply" {
		t.Fatalf("expected sidecar reply, got %q", reply)
	}
	if main.calls != MaxRetries+1 {
		t.Fatalf("expected main to be tried %d times, got %d", MaxRetries+1, main.calls)
	}
	if side.calls != 1 {
		t.Fatalf("expected sidecar to be tried once, got %d", side.calls)
	}
}

func TestNoSidecarFallbackOnNonConnectionError(t *testing.T) {
	main := &fallbackMainClient{err: errors.New("model not found")}
	side := &fallbackSidecarClient{reply: "sidecar reply"}
	ag := newFallbackAgent(t, main, side)

	_, _, err := ag.chatOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("expected main error to be returned, got %v", err)
	}
	if side.calls != 0 {
		t.Fatalf("expected no sidecar call, got %d", side.calls)
	}
}

func TestSidecarFallbackFailureReturned(t *testing.T) {
	main := &fallbackMainClient{err: errors.New("connection refused")}
	side := &fallbackSidecarClient{err: errors.New("sidecar also down")}
	ag := newFallbackAgent(t, main, side)

	_, _, err := ag.chatOnce(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "sidecar fallback failed") {
		t.Fatalf("expected sidecar fallback failure in error, got %v", err)
	}
}

func TestSidecarDisabledNoFallback(t *testing.T) {
	main := &fallbackMainClient{err: errors.New("connection refused")}
	ag := newFallbackAgent(t, main, nil)

	_, _, err := ag.chatOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("expected main error, got %v", err)
	}
}
