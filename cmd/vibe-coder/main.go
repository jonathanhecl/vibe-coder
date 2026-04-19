package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/prompt"
	"github.com/jonathanhecl/vibe-coder/internal/version"
)

func main() {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	binName := filepath.Base(os.Args[0])
	if cfg.ShowHelp {
		fmt.Fprint(os.Stdout, config.Usage(binName))
		return
	}

	if cfg.ShowVer {
		fmt.Fprintf(os.Stdout, "vibe-coder %s\n", version.Value)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := ollama.NewHTTP(cfg.OllamaHost)

	if cfg.ListSessions {
		versionInfo, err := client.Version(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to connect to Ollama: %v\n", err)
			os.Exit(1)
		}
		models, err := client.Tags(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to list Ollama models: %v\n", err)
			os.Exit(1)
		}

		fmt.Fprintf(os.Stdout, "Ollama %s\n", versionInfo)
		if len(models) == 0 {
			fmt.Fprintln(os.Stdout, "No downloaded models found yet.")
			return
		}
		fmt.Fprintln(os.Stdout, "Available models:")
		for _, model := range models {
			fmt.Fprintf(os.Stdout, "- %s\n", model.Name)
		}
		return
	}

	if cfg.Prompt != "" {
		systemPrompt := prompt.Build(cfg)
		stream, err := client.Chat(ctx, ollama.ChatRequest{
			Model: cfg.Model,
			Messages: []ollama.Message{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: cfg.Prompt},
			},
			Stream: true,
			Options: ollama.ChatOptions{
				NumCtx:      cfg.ContextWindow,
				NumPredict:  cfg.MaxTokens,
				Temperature: cfg.Temperature,
			},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		for chunk := range stream {
			if chunk.Err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", chunk.Err)
				os.Exit(1)
			}
			if chunk.Delta != "" {
				fmt.Fprint(os.Stdout, chunk.Delta)
			}
			if chunk.Done {
				fmt.Fprintln(os.Stdout)
				return
			}
		}
		fmt.Fprintln(os.Stdout)
		return
	}

	fmt.Fprintln(os.Stdout, "MVP bootstrap complete: agent wiring is coming in next milestones.")
}

