//go:build !rag

package main

import (
	"context"
	"fmt"

	"github.com/jonathanhecl/vibe-coder/internal/agent"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

func configureRAG(_ context.Context, cfg *config.Config, _ ollama.Client, _ *agent.Agent) (bool, string, error) {
	if cfg.RAG || cfg.RAGIndex != "" {
		return true, "", fmt.Errorf("RAG requires build tag `rag`")
	}
	return false, "", nil
}
