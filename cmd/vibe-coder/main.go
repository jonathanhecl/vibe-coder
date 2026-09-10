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
	"github.com/jonathanhecl/vibe-coder/internal/contextfiles"
	"github.com/jonathanhecl/vibe-coder/internal/logger"
	"github.com/jonathanhecl/vibe-coder/internal/mcp"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/onboarding"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/sidecar"
	"github.com/jonathanhecl/vibe-coder/internal/skills"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
	"github.com/jonathanhecl/vibe-coder/internal/version"
	"github.com/jonathanhecl/vibe-coder/internal/watcher"
)

const maxExternalEmptyRetries = 3

func main() {
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		cfg, err := config.Load(nil)
		if err != nil {
			exitWithError(err)
		}
		if err := mcp.RunCLI(cfg.ConfigDir, cfg.Cwd, os.Args[2:]); err != nil {
			exitWithError(err)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "skill" || os.Args[1] == "skills") {
		cfg, err := config.Load(nil)
		if err != nil {
			exitWithError(err)
		}
		if err := skills.RunCLI(cfg.ConfigDir, cfg.Cwd, os.Args[2:]); err != nil {
			exitWithError(err)
		}
		return
	}

	args, persistModelSettings := extractPersistDirective(os.Args[1:])
	cfg, err := config.Load(args)
	if err != nil {
		exitWithError(err)
	}

	closer, logErr := logger.Init(cfg.ConfigDir)
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to initialize log file: %v\n", logErr)
	} else if closer != nil {
		defer closer.Close()
	}

	logger.Infof("vibe-coder %s starting", version.Value)
	logger.Infof("CLI args: %v", os.Args)
	logger.Infof("ConfigDir: %s, ConfigFile: %s", cfg.ConfigDir, cfg.ConfigFile)
	logger.Infof("OllamaHost: %s, Model: %s, UI: %s", cfg.OllamaHost, cfg.Model, cfg.UI)

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
		logger.Infof("Onboarding flow detected, starting...")
		if err := onboarding.RunFirstRun(context.Background(), cfg, version.Value, os.Stdin, os.Stdout); err != nil {
			if errors.Is(err, onboarding.ErrInterrupted) {
				logger.Infof("Onboarding interrupted by user.")
				fmt.Fprintln(os.Stdout, "\nBye.")
				os.Exit(130)
			}
			exitWithError(fmt.Errorf("first-run setup failed: %w", err))
		}
		logger.Infof("Onboarding flow completed successfully.")
	}

	client := ollama.NewHTTP(cfg.OllamaHost)
	sess := session.New(cfg)
	sess.SetClient(client)
	resolveVisionSupport(cfg, client)
	ctxStore := contextfiles.NewStore()
	preloadSessionContexts(cfg, ctxStore)
	ui, err := tui.NewFromMode(cfg)
	if err != nil {
		exitWithError(err)
	}
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	sub := tools.NewSubAgentTool(cfg, client)
	reg.Register(sub)
	reg.Register(tools.NewParallelAgentsTool(sub))
	perm := permissions.NewManager(cfg)
	// One shared sidecar pool for the agent and the DescribeImage tool so
	// vision second-opinions respect the same load controls as summaries.
	sidePool := sidecar.New(cfg, client)
	reg.Register(tools.NewDescribeImageTool(cfg, client, sidePool))

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
	ag.SetContextStore(ctxStore)
	ag.SetSidecar(sidePool)
	fileWatcher := watcher.New(cfg.Cwd)
	ag.SetWatcher(fileWatcher)
	defer fileWatcher.Close()
	defer ui.Stop()

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	installSignalHandler(ui, sess, rootCancel)

	logger.Infof("Configuring RAG...")
	ragHandled, ragMsg, ragErr := configureRAG(rootCtx, cfg, client, ag)
	if ragErr != nil {
		exitWithError(ragErr)
	}
	if ragHandled {
		logger.Infof("RAG hand-off completed.")
		if strings.TrimSpace(ragMsg) != "" {
			fmt.Fprintln(os.Stdout, ragMsg)
		}
		return
	}

	if cfg.ListSessions {
		logger.Infof("Listing available models from host...")
		if err := printAvailableModels(rootCtx, client); err != nil {
			exitWithError(err)
		}
		return
	}

	if cfg.Resume {
		logger.Infof("Resuming session configuration...")
		if err := resumeConfiguredSession(cfg, sess); err != nil {
			exitWithError(err)
		}
		restoreSessionContexts(sess, ctxStore)
	}
	sess.SetPinnedContexts(ctxStore.Paths())

	bannerPrinted := false
	if cfg.Prompt != "" {
		logger.Infof("Running one-shot prompt flow: %q", cfg.Prompt)
		shouldContinue, err := runInitialPrompt(rootCtx, cfg, ag, sess, ui)
		if err != nil {
			exitWithError(err)
		}
		bannerPrinted = true
		if !shouldContinue {
			logger.Infof("One-shot prompt completed without continuation.")
			return
		}
		cfg.Prompt = ""
	}

	if !bannerPrinted {
		fmt.Fprint(os.Stdout, startupBanner(cfg, sess.ID(), tui.NewStyle(os.Stdout)))
	}
	logger.Infof("Entering interactive REPL loop.")
	runInteractiveREPL(rootCtx, cfg, client, ag, sess, perm, ui, ctxStore)
	logger.Infof("Exiting interactive REPL loop, finishing execution.")
}

func exitWithError(err error) {
	// Centralizes fatal CLI errors so the top-level flow stays readable.
	logger.Errorf("Fatal error: %v", err)
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

// resolveVisionSupport detects once, at startup, whether the active model
// advertises vision capability. It is best-effort: on any failure the agent
// runs with VisionKnown=false and stays honest about trying.
func resolveVisionSupport(cfg *config.Config, client ollama.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	models, err := client.Tags(ctx)
	if err != nil {
		logger.Infof("Vision detection skipped (tags failed): %v", err)
		return
	}
	cfg.VisionByModel = make(map[string]bool, len(models))
	cfg.ThinkingByModel = make(map[string]bool, len(models))
	for _, m := range models {
		name := strings.ToLower(strings.TrimSpace(m.Name))
		if name != "" {
			cfg.VisionByModel[name] = m.SupportsVision()
			cfg.ThinkingByModel[name] = m.SupportsThinking()
		}
	}
	applyVisionForModel(cfg)
	applyThinkingForModel(cfg)
}

// applyVisionForModel refreshes the vision flags for the active model from
// the cached Tags map. Unknown models leave VisionKnown=false. The sidecar
// model resolves through the same map; it only changes via CLI/env, so
// startup detection is enough for the whole session.
func applyVisionForModel(cfg *config.Config) {
	available, known := ollama.LookupVision(cfg.VisionByModel, cfg.Model)
	cfg.VisionAvailable = available
	cfg.VisionKnown = known
	logger.Infof("Vision support for model %q: available=%t known=%t", cfg.Model, available, known)
	if strings.TrimSpace(cfg.SidecarModel) == "" {
		cfg.SidecarVisionAvailable, cfg.SidecarVisionKnown = false, false
		return
	}
	sideAvailable, sideKnown := ollama.LookupVision(cfg.VisionByModel, cfg.SidecarModel)
	cfg.SidecarVisionAvailable, cfg.SidecarVisionKnown = sideAvailable, sideKnown
	logger.Infof("Vision support for sidecar %q: available=%t known=%t", cfg.SidecarModel, sideAvailable, sideKnown)
}

// applyThinkingForModel refreshes the thinking-capability flags for the
// active model from the cached Tags map. Unknown models leave
// ThinkingKnown=false.
func applyThinkingForModel(cfg *config.Config) {
	supported, known := ollama.LookupVision(cfg.ThinkingByModel, cfg.Model)
	cfg.ThinkingSupported = supported
	cfg.ThinkingKnown = known
	logger.Infof("Thinking support for model %q: supported=%t known=%t", cfg.Model, supported, known)
}

// preloadSessionContexts pins every --context file (accumulated) into the
// shared store before the session starts. Failures warn but keep the CLI
// usable so one bad path does not kill the whole run.
func preloadSessionContexts(cfg *config.Config, store *contextfiles.Store) {
	for _, p := range cfg.ContextFiles {
		entry, _, err := store.Add(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping context file %q: %v\n", p, err)
			continue
		}
		fmt.Fprintf(os.Stdout, "Pinned context: %s\n", entry.Name)
	}
}

// restoreSessionContexts reloads files pinned by a resumed session into the
// shared store, unioned with any --context files already preloaded.
func restoreSessionContexts(sess *session.Session, store *contextfiles.Store) {
	for _, p := range sess.PinnedContexts() {
		if _, _, err := store.Add(p); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pinned context %q could not be reloaded: %v\n", p, err)
		}
	}
}
