package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

func TestStartupConnectionFailed(t *testing.T) {
	t.Parallel()

	transport := fmt.Errorf("ollama tags: %w", &url.Error{
		Op:  "Get",
		URL: "http://127.0.0.1:11434/api/tags",
		Err: errors.New("connection refused"),
	})
	deadline := fmt.Errorf("ollama version: %w", context.DeadlineExceeded)
	deadlineURL := &url.Error{
		Op:  "Get",
		URL: "http://127.0.0.1:11434/api/version",
		Err: context.DeadlineExceeded,
	}
	cancelled := fmt.Errorf("ollama tags: %w", &url.Error{
		Op:  "Get",
		URL: "http://127.0.0.1:11434/api/tags",
		Err: context.Canceled,
	})
	httpStatus := errors.New("ollama tags failed: 404 Not Found")

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"wrapped url transport error", transport, true},
		{"context deadline exceeded", deadline, true},
		{"url error wrapping deadline", deadlineURL, true},
		{"url error wrapping cancellation", cancelled, false},
		{"plain context cancellation", context.Canceled, false},
		{"plain http status error", httpStatus, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := startupConnectionFailed(tc.err); got != tc.want {
				t.Fatalf("startupConnectionFailed(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}

func TestStartupOfflineModels(t *testing.T) {
	t.Parallel()

	// Shared OpenAI provider backing the rabbit and vision roles: both roles
	// must resolve to a single client, so the startup probe hits /v1/models
	// exactly once.
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{}})
	}))
	defer srv.Close()

	// Embedding provider whose endpoint is already gone: the Version probe
	// must fail at transport level and mark the role offline.
	deadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := deadSrv.URL
	deadSrv.Close()

	cfg := &config.Config{
		Model:        "main-model",
		SidecarModel: "helper-model",
		Providers: map[string]config.Provider{
			"fast": {Name: "fast", Kind: config.ProviderOpenAI, Endpoint: srv.URL, APIKey: "test-key"},
			"dead": {Name: "dead", Kind: config.ProviderOllama, Endpoint: deadURL},
		},
		RoleModels: map[config.Role]config.RoleModel{
			config.RoleRabbit:    {Provider: "fast", Model: "fast-model"},
			config.RoleVision:    {Provider: "fast", Model: "vision-model"},
			config.RoleEmbedding: {Provider: "dead", Model: "embed-model"},
		},
	}

	mainClient := ollama.NewHTTP("http://127.0.0.1:9")
	sideClient := ollama.NewHTTP("http://127.0.0.1:9")
	transportErr := fmt.Errorf("ollama tags: %w", &url.Error{
		Op:  "Get",
		URL: "http://side.local/api/tags",
		Err: errors.New("connection refused"),
	})
	checks := map[ollama.Client]error{
		mainClient: nil,
		sideClient: transportErr,
	}
	decisionErr := fmt.Errorf("decision: health check failed: %w", &url.Error{
		Op:  "Get",
		URL: "http://decision.local/healthz",
		Err: errors.New("connection refused"),
	})

	offline := startupOfflineModels(context.Background(), cfg, mainClient, sideClient, checks, decisionErr)

	want := map[config.Role]bool{
		config.RoleSidecar:   true,
		config.RoleEmbedding: true,
		config.RoleDecision:  true,
	}
	if len(offline) != len(want) {
		t.Fatalf("offline = %v, want exactly %v", offline, want)
	}
	for role := range want {
		if !offline[role] {
			t.Fatalf("expected %s to be marked offline, got %v", role, offline)
		}
	}
	for _, role := range []config.Role{config.RoleMain, config.RoleRabbit, config.RoleVision} {
		if offline[role] {
			t.Fatalf("did not expect %s to be marked offline, got %v", role, offline)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("shared provider probed %d times, want 1", got)
	}
}

func TestStartupOfflineModelsSkipsInactiveSidecar(t *testing.T) {
	t.Parallel()

	transportErr := fmt.Errorf("ollama tags: %w", &url.Error{
		Op:  "Get",
		URL: "http://side.local/api/tags",
		Err: errors.New("connection refused"),
	})

	t.Run("disabled sidecar", func(t *testing.T) {
		mainClient := ollama.NewHTTP("http://127.0.0.1:9")
		sideClient := ollama.NewHTTP("http://127.0.0.1:9")
		cfg := &config.Config{
			Model:           "main-model",
			SidecarModel:    "helper-model",
			SidecarDisabled: true,
		}
		checks := map[ollama.Client]error{
			mainClient: nil,
			sideClient: transportErr,
		}
		offline := startupOfflineModels(context.Background(), cfg, mainClient, sideClient, checks, nil)
		if len(offline) != 0 {
			t.Fatalf("expected no offline roles for a disabled sidecar, got %v", offline)
		}
	})

	t.Run("unbound sidecar", func(t *testing.T) {
		mainClient := ollama.NewHTTP("http://127.0.0.1:9")
		sideClient := ollama.NewHTTP("http://127.0.0.1:9")
		cfg := &config.Config{Model: "main-model"}
		checks := map[ollama.Client]error{mainClient: nil}
		offline := startupOfflineModels(context.Background(), cfg, mainClient, sideClient, checks, nil)
		if len(offline) != 0 {
			t.Fatalf("expected no offline roles for an unbound sidecar, got %v", offline)
		}
	})
}
