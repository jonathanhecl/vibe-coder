package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/agent"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/mcp"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/onboarding"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
	"github.com/jonathanhecl/vibe-coder/internal/version"
	"github.com/jonathanhecl/vibe-coder/internal/watcher"
)

const maxExternalEmptyRetries = 3

func main() {
	args, persistModelSettings := extractPersistDirective(os.Args[1:])
	cfg, err := config.Load(args)
	if err != nil {
		exitWithError(err)
	}

	if persistModelSettings {
		cfg.PersistSidecarOffFromSave(true)
		if err := config.SaveModelSettings(cfg); err != nil {
			exitWithError(fmt.Errorf("failed to save model settings: %w", err))
		}
		fmt.Fprintf(os.Stdout, "Saved model settings to %s\n", cfg.ConfigFile)
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

	if shouldRunFirstRunOnboarding(cfg, persistModelSettings) {
		if err := onboarding.RunFirstRun(context.Background(), cfg, version.Value, os.Stdin, os.Stdout); err != nil {
			if errors.Is(err, onboarding.ErrInterrupted) {
				fmt.Fprintln(os.Stdout, "\nBye.")
				os.Exit(130)
			}
			exitWithError(fmt.Errorf("first-run setup failed: %w", err))
		}
	}

	client := ollama.NewHTTP(cfg.OllamaHost)
	sess := session.New(cfg)
	sess.SetClient(client)
	ui, err := tui.NewFromMode(cfg.UI)
	if err != nil {
		exitWithError(err)
	}
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	sub := tools.NewSubAgentTool(cfg, client)
	reg.Register(sub)
	reg.Register(tools.NewParallelAgentsTool(sub))
	perm := permissions.NewManager(cfg)

	mcpCtx, mcpCancel := mcp.DefaultInitContext()
	defer mcpCancel()
	mcpClients, mcpTools, mcpErr := mcp.InitAndWrapAll(mcpCtx, cfg.ConfigDir, cfg.Cwd)
	if mcpErr != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to initialize MCP tools: %v\n", mcpErr)
	}
	for _, tool := range mcpTools {
		reg.Register(tool)
		perm.AddAskTool(tool.Name())
	}
	defer func() {
		for _, c := range mcpClients {
			c.Stop()
		}
	}()

	ag := agent.New(cfg, client, reg, perm, sess, ui)
	fileWatcher := watcher.New(cfg.Cwd)
	ag.SetWatcher(fileWatcher)
	defer fileWatcher.Close()
	defer ui.Stop()

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	installSignalHandler(ui, sess, rootCancel)

	ragHandled, ragMsg, ragErr := configureRAG(rootCtx, cfg, client, ag)
	if ragErr != nil {
		exitWithError(ragErr)
	}
	if ragHandled {
		if strings.TrimSpace(ragMsg) != "" {
			fmt.Fprintln(os.Stdout, ragMsg)
		}
		return
	}

	if cfg.ListSessions {
		if err := printAvailableModels(rootCtx, client); err != nil {
			exitWithError(err)
		}
		return
	}

	if cfg.Resume {
		if err := resumeConfiguredSession(cfg, sess); err != nil {
			exitWithError(err)
		}
	}

	bannerPrinted := false
	if cfg.Prompt != "" {
		shouldContinue, err := runInitialPrompt(rootCtx, cfg, ag, sess, ui)
		if err != nil {
			exitWithError(err)
		}
		bannerPrinted = true
		if !shouldContinue {
			return
		}
		cfg.Prompt = ""
	}

	if !bannerPrinted {
		fmt.Fprint(os.Stdout, startupBanner(cfg, sess.ID(), tui.NewStyle(os.Stdout)))
	}
	runInteractiveREPL(rootCtx, cfg, client, ag, sess, perm, ui)
}

func exitWithError(err error) {
	// Centralizes fatal CLI errors so the top-level flow stays readable.
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func printAvailableModels(rootCtx context.Context, client ollama.Client) error {
	ctx, cancel := context.WithTimeout(rootCtx, 2*time.Minute)
	defer cancel()
	versionInfo, err := client.Version(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to Ollama: %w", err)
	}
	models, err := client.Tags(ctx)
	if err != nil {
		return fmt.Errorf("failed to list Ollama models: %w", err)
	}

	fmt.Fprintf(os.Stdout, "Ollama %s\n", versionInfo)
	if len(models) == 0 {
		fmt.Fprintln(os.Stdout, "No downloaded models found yet.")
		return nil
	}
	fmt.Fprintln(os.Stdout, "Available models:")
	for _, model := range models {
		fmt.Fprintf(os.Stdout, "- %s\n", model.Name)
	}
	return nil
}

func resumeConfiguredSession(cfg *config.Config, sess *session.Session) error {
	if cfg.SessionID != "" {
		if err := sess.Load(cfg.SessionID); err != nil {
			return fmt.Errorf("failed to load session %q: %w", cfg.SessionID, err)
		}
		fmt.Fprintf(os.Stdout, "Resumed session %s\n", sess.ID())
		return nil
	}
	ok, err := sess.LoadByProject()
	if err != nil {
		return fmt.Errorf("failed to resume session by project: %w", err)
	}
	if ok {
		fmt.Fprintf(os.Stdout, "Resumed project session %s\n", sess.ID())
	}
	return nil
}
