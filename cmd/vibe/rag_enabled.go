//go:build rag

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/agent"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/rag"
)

func configureRAG(ctx context.Context, cfg *config.Config, client ollama.Client, ag *agent.Agent) (bool, string, error) {
	if !cfg.RAG && strings.TrimSpace(cfg.RAGIndex) == "" {
		return false, "", nil
	}
	engine, err := rag.NewEngine(cfg, client)
	if err != nil {
		return true, "", err
	}
	if strings.TrimSpace(cfg.RAGIndex) != "" {
		root := cfg.RAGIndex
		if strings.TrimSpace(root) == "" {
			root = cfg.Cwd
		}
		if err := engine.IndexPath(ctx, root); err != nil {
			_ = engine.Close()
			return true, "", err
		}
		stats := engine.Stats()
		_ = engine.Close()
		msg := fmt.Sprintf("RAG index complete: files=%d chunks=%d db=%.1f KiB", stats.Files, stats.Chunks, stats.DBSizeKiB)
		return true, msg, nil
	}
	ag.SetRAG(engine)
	return false, "", nil
}
