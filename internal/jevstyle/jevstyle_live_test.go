package jevstyle

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

func TestLiveJevstyleRemote(t *testing.T) {
	const host = "http://192.168.0.33:11434"
	const model = "huggingface.co/chaoliangUNSW/Jev-Style-Qwen3.5-2B-Decision-GGUF:Q4_K_M"

	// Quick check if the remote host is reachable; skip if offline so CI doesn't fail.
	clientHTTP := &http.Client{Timeout: 2 * time.Second}
	resp, err := clientHTTP.Get(host + "/api/tags")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Skip("skipping live test: 192.168.0.33 is not reachable")
	}
	_ = resp.Body.Close()

	cfg := &config.Config{
		OllamaHost:    host,
		JevstyleModel: model,
	}
	ollamaClient := ollama.NewHTTP(host)
	jevClient := New(cfg, ollamaClient)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	decResp, err := jevClient.Decide(ctx, DecisionRequest{
		State:    "The codebase is written in Go.",
		Question: "Which programming language is used?",
		Options:  []string{"Python", "Go", "Rust"},
	})
	if err != nil {
		t.Fatalf("live decide error: %v", err)
	}

	if decResp.Choice != "B" || decResp.Option != "Go" {
		t.Fatalf("unexpected choice: got %+v, want Choice=B, Option=Go", decResp)
	}
}
