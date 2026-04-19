package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/prompt"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/slash"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
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

	client := ollama.NewHTTP(cfg.OllamaHost)
	sess := session.New(cfg)
	ui := tui.NewPlain()
	defer ui.Stop()

	rootCtx, rootCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer rootCancel()

	if cfg.ListSessions {
		ctx, cancel := context.WithTimeout(rootCtx, 2*time.Minute)
		defer cancel()
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

	if cfg.Resume {
		if cfg.SessionID != "" {
			if err := sess.Load(cfg.SessionID); err != nil {
				fmt.Fprintf(os.Stderr, "error: failed to load session %q: %v\n", cfg.SessionID, err)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stdout, "Resumed session %s\n", sess.ID())
		} else {
			ok, err := sess.LoadByProject()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: failed to resume session by project: %v\n", err)
				os.Exit(1)
			}
			if ok {
				fmt.Fprintf(os.Stdout, "Resumed project session %s\n", sess.ID())
			}
		}
	}

	if cfg.Prompt != "" {
		if err := runPrompt(rootCtx, cfg, client, ui, sess, cfg.Prompt); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := sess.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to save session: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Fprintf(os.Stdout, "Session started: %s\n", sess.ID())
	slashCtx := &slash.Ctx{
		Cfg:     cfg,
		Session: sess,
		Out:     os.Stdout,
	}

	for {
		line, err := ui.GetInput("> ")
		if err != nil {
			if err := sess.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "error: failed to save session: %v\n", err)
			}
			fmt.Fprintln(os.Stdout, "\nBye.")
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		handled, shouldExit, err := slash.Dispatch(slashCtx, line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		if shouldExit {
			fmt.Fprintln(os.Stdout, "Bye.")
			return
		}
		if handled {
			continue
		}

		if err := runPrompt(rootCtx, cfg, client, ui, sess, line); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
	}
}

func runPrompt(
	rootCtx context.Context,
	cfg *config.Config,
	client ollama.Client,
	ui *tui.PlainUI,
	sess *session.Session,
	userPrompt string,
) error {
	ctx, cancel := context.WithTimeout(rootCtx, 2*time.Minute)
	defer cancel()

	if err := ui.StartESCMonitor(cancel); err != nil {
		return err
	}
	defer ui.StopESCMonitor()

	systemPrompt := prompt.Build(cfg)
	sess.AddUser(userPrompt)
	stream, err := client.Chat(ctx, ollama.ChatRequest{
		Model: cfg.Model,
		Messages: []ollama.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Stream: true,
		Options: ollama.ChatOptions{
			NumCtx:      cfg.ContextWindow,
			NumPredict:  cfg.MaxTokens,
			Temperature: cfg.Temperature,
		},
	})
	if err != nil {
		return err
	}

	var response strings.Builder
	for chunk := range stream {
		if chunk.Err != nil {
			if chunk.Err == context.Canceled {
				ui.EndAssistant()
				sess.AddAssistant("[Cancelled by user]")
				return nil
			}
			return chunk.Err
		}
		if chunk.Delta != "" {
			ui.StreamAssistant(chunk.Delta)
			response.WriteString(chunk.Delta)
		}
		if chunk.Done {
			ui.EndAssistant()
			sess.AddAssistant(response.String())
			return nil
		}
	}
	ui.EndAssistant()
	if response.Len() > 0 {
		sess.AddAssistant(response.String())
	}
	return nil
}
