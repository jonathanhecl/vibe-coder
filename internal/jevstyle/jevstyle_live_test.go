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

	// 2. Live Command Safety
	dangerous, err := jevClient.IsCommandDangerous(ctx, "rm -rf .git")
	if err != nil || !dangerous {
		t.Fatalf("expected rm -rf .git to be dangerous, got %t (err: %v)", dangerous, err)
	}
	dangerous, err = jevClient.IsCommandDangerous(ctx, "git status")
	if err != nil || dangerous {
		t.Fatalf("expected git status to be safe, got %t (err: %v)", dangerous, err)
	}

	// 3. Live DisambiguatePath
	cands := []string{
		"/mnt/cloud/Clouds/Github/vibe-coder/cmd/vibe/main.go",
		"/mnt/cloud/Clouds/Github/vibe-coder/internal/config/flags.go",
	}
	chosen, ok, err := jevClient.DisambiguatePath(ctx, "target file: flags.go for parsing CLI options", cands)
	if err != nil || !ok || chosen != cands[1] {
		t.Fatalf("expected flags.go chosen, got chosen=%q ok=%t err=%v", chosen, ok, err)
	}

	// 4. Live CheckGoalCompletion
	done, err := jevClient.CheckGoalCompletion(ctx, "add flag --verbose to CLI", "All code written, flag added to flags.go, tests passing.")
	if err != nil || !done {
		t.Fatalf("expected goal to be complete, got %t (err: %v)", done, err)
	}
}
