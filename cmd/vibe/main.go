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
	"github.com/jonathanhecl/vibe-coder/internal/decision"
	"github.com/jonathanhecl/vibe-coder/internal/logger"
	"github.com/jonathanhecl/vibe-coder/internal/mcp"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/onboarding"
	"github.com/jonathanhecl/vibe-coder/internal/openai"
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

	logger.Infof("vibe %s starting", version.Value)
	// Args are redacted: --env values and other secrets must never land in the log.
	logger.Infof("CLI args: %v", logger.RedactArgs(os.Args))
	logger.Infof("ConfigDir: %s, ConfigFile: %s", cfg.ConfigDir, cfg.ConfigFile)
	logger.Infof("ModelEndpoint: %s (openai=%t), Model: %s, UI: %s", cfg.EffectiveModelEndpoint(), cfg.IsModelOpenAI(), cfg.Model, cfg.UI)
	if cfg.SidecarInUse() {
		logger.Infof("SidecarEndpoint: %s (openai=%t), SidecarModel: %s", cfg.EffectiveSidecarEndpoint(), cfg.IsSidecarOpenAI(), cfg.SidecarModel)
	}
	if strings.TrimSpace(cfg.DecisionModel) != "" {
		logger.Infof("DecisionModel: %s", cfg.DecisionModel)
	}
	if strings.TrimSpace(cfg.DecisionEndpoint) != "" {
		logger.Infof("DecisionEndpoint: %s", cfg.DecisionEndpoint)
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
		fmt.Fprintf(os.Stdout, "vibe %s\n", version.Value)
		return
	}

	if cfg.LegacyJevstyle {
		const msg = "JEVSTYLE_* settings are no longer supported; use DECISION_MODEL / DECISION_ENDPOINT (see README)"
		logger.Infof("warning: %s", msg)
		fmt.Fprintf(os.Stderr, "warning: %s\n", msg)
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

	client := createLLMClient(cfg.EffectiveModelEndpoint(), cfg.ModelApiKey, cfg.IsModelOpenAI())
	var sideClient ollama.Client
	if cfg.EffectiveSidecarEndpoint() == cfg.EffectiveModelEndpoint() && cfg.IsSidecarOpenAI() == cfg.IsModelOpenAI() {
		sideClient = client
	} else {
		sideClient = createLLMClient(cfg.EffectiveSidecarEndpoint(), cfg.SidecarApiKey, cfg.IsSidecarOpenAI())
	}

	sess := session.New(cfg)
	sess.SetClient(client)
	resolveVisionSupport(cfg, client, sideClient)
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
	var decider decision.Decider
	if decisionClient := decision.FromConfig(cfg); decisionClient != nil {
		checkCtx, checkCancel := context.WithTimeout(context.Background(), 5*time.Second)
		info, checkErr := decisionClient.Check(checkCtx)
		checkCancel()
		if checkErr != nil {
			fmt.Fprintf(os.Stderr, "warning: decision model check failed: %v\n", checkErr)
			logger.Infof("decision model check failed: %v", checkErr)
		} else {
			logger.Infof("decision backend: %s (model: %s)", info.Backend, info.Model)
		}
		decider = decisionClient
	}
	if decider != nil && decider.Enabled() {
		perm.SetSafetyDecider(decider)
		reg.Register(tools.NewDecideTool(decider))
	}
	// One shared sidecar pool for the agent and the DescribeImage tool so
	// vision second-opinions respect the same load controls as summaries.
	sidePool := sidecar.New(cfg, sideClient)
	if cfg.DecisionInUse() {
		sidePool.SetDecider(decider)
	}
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
	if cfg.DecisionInUse() {
		ag.SetDecider(decider)
	}
	fileWatcher := watcher.New(cfg.Cwd)
	ag.SetWatcher(fileWatcher)
	defer fileWatcher.Close()
	defer ui.Stop()

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	installSignalHandler(ui, sess, globalInterrupter)

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

	resumed, err := resolveSession(cfg, sess)
	if err != nil {
		exitWithError(err)
	}
	if resumed {
		restoreSessionContexts(sess, ctxStore)
		// Restore the durable TODO/task state so a resumed autonomous run
		// knows what is already done.
		ag.RestoreWorkState(sess.WorkState())
		if ag.MissionActive() {
			m := ag.MissionSnapshot()
			resumeGoal := strings.TrimSpace(m.Goal)
			if resumeGoal == "" {
				resumeGoal = "the previous goal"
			}
			fmt.Fprintf(os.Stdout, "Active mission restored (%s). Send any message (e.g. \"continue\") to keep working autonomously.\n", resumeGoal)
		}
	}
	sess.SetPinnedContexts(ctxStore.Paths())

	bannerPrinted := false
	if cfg.Prompt != "" {
		logger.Infof("Running one-shot prompt flow: %q", cfg.Prompt)
		shouldContinue, err := runInitialPrompt(rootCtx, cfg, ag, sess, ui, resumed)
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
		fmt.Fprint(os.Stdout, startupBanner(cfg, sess.ID(), resumed, tui.NewStyle(os.Stdout)))
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

func resolveSession(cfg *config.Config, sess *session.Session) (bool, error) {
	if cfg == nil || sess == nil || cfg.Temporal {
		return false, nil
	}
	if cfg.SessionID != "" {
		if err := sess.Load(cfg.SessionID); err != nil {
			return false, fmt.Errorf("failed to load session %q: %w", cfg.SessionID, err)
		}
		return true, nil
	}
	// Explicit isolated session requested (--isolated)
	if cfg.Isolated {
		if !cfg.NewSession {
			ok, err := sess.LoadIsolated()
			if err != nil {
				return false, fmt.Errorf("failed to load isolated session: %w", err)
			}
			if ok {
				return true, nil
			}
		} else {
			_ = sess.ClearIsolated()
		}
		return false, nil
	}
	// Auto-detect isolated session in current directory
	if !cfg.NewSession && session.HasIsolatedSession(cfg.Cwd) {
		ok, err := sess.LoadIsolated()
		if err == nil && ok {
			cfg.Isolated = true
			return true, nil
		}
	}
	if cfg.Resume {
		ok, err := sess.LoadByProject()
		if err != nil {
			return false, fmt.Errorf("failed to resume session by project: %w", err)
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "No previous session found for this project; started a new session.")
			return false, nil
		}
		return true, nil
	}
	if !cfg.NewSession {
		ok, err := sess.LoadByProject()
		if err == nil && ok {
			return true, nil
		}
	}
	return false, nil
}

func createLLMClient(endpoint, apiKey string, isOpenAI bool) ollama.Client {
	if isOpenAI {
		var opts []openai.Option
		if apiKey != "" {
			opts = append(opts, openai.WithAPIKey(apiKey))
		}
		return openai.New(endpoint, opts...)
	}
	return ollama.NewHTTPWithAPIKey(endpoint, apiKey)
}

// resolveVisionSupport detects once, at startup, whether the active model
// advertises vision capability. It is best-effort: on any failure the agent
// runs with VisionKnown=false and stays honest about trying.
func resolveVisionSupport(cfg *config.Config, client ollama.Client, extraClients ...ollama.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if cfg.VisionByModel == nil {
		cfg.VisionByModel = make(map[string]bool)
	}
	if cfg.ThinkingByModel == nil {
		cfg.ThinkingByModel = make(map[string]bool)
	}
	if cfg.ToolsByModel == nil {
		cfg.ToolsByModel = make(map[string]bool)
	}
	models, err := client.Tags(ctx)
	if err != nil {
		logger.Infof("Vision detection skipped for main client (tags failed): %v", err)
	} else {
		for _, m := range models {
			name := strings.ToLower(strings.TrimSpace(m.Name))
			// Absent capability metadata is "unknown", not "unsupported":
			// recording false here would silently disable tools/vision.
			if name == "" || !m.CapabilitiesKnown {
				continue
			}
			cfg.VisionByModel[name] = m.SupportsVision()
			cfg.ThinkingByModel[name] = m.SupportsThinking()
			cfg.ToolsByModel[name] = m.SupportsTools()
		}
		probeModelCapabilities(ctx, cfg, client, models)
	}
	for _, extra := range extraClients {
		if extra == nil || extra == client || strings.TrimSpace(cfg.SidecarModel) == "" {
			continue
		}
		if extraModels, err := extra.Tags(ctx); err == nil {
			for _, m := range extraModels {
				name := strings.ToLower(strings.TrimSpace(m.Name))
				if name == "" || !m.CapabilitiesKnown {
					continue
				}
				cfg.VisionByModel[name] = m.SupportsVision()
				cfg.ThinkingByModel[name] = m.SupportsThinking()
				cfg.ToolsByModel[name] = m.SupportsTools()
			}
			probeModelCapabilities(ctx, cfg, extra, extraModels)
		} else {
			logger.Infof("Vision detection skipped for extra client (tags failed): %v", err)
		}
	}
	applyVisionForModel(cfg)
	applyThinkingForModel(cfg)
	applyToolsForModel(cfg)
}

// probeModelCapabilities resolves "unknown" capability state for the active
// and sidecar models via Show. Other models stay unknown until used.
func probeModelCapabilities(ctx context.Context, cfg *config.Config, client ollama.Client, models []ollama.Model) {
	inspector, ok := client.(interface {
		Show(context.Context, string) (ollama.Model, error)
	})
	if !ok {
		return
	}
	probed := map[string]bool{}
	for _, want := range []string{cfg.Model, cfg.SidecarModel} {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		key := strings.ToLower(want)
		if probed[key] {
			continue
		}
		// If capabilities for this model are already known, skip probing.
		if _, known := ollama.LookupVision(cfg.VisionByModel, want); known {
			continue
		}
		probed[key] = true
		info, err := inspector.Show(ctx, want)
		if err != nil || !info.CapabilitiesKnown {
			continue
		}
		cfg.VisionByModel[key] = info.SupportsVision()
		cfg.ThinkingByModel[key] = info.SupportsThinking()
		cfg.ToolsByModel[key] = info.SupportsTools()
		logger.Infof("Capability probe for model %q via Show: vision=%t thinking=%t tools=%t",
			key, info.SupportsVision(), info.SupportsThinking(), info.SupportsTools())
	}
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

// applyToolsForModel refreshes the native tool-calling flags for the active
// model from the cached Tags map. Unknown models leave ToolsKnown=false,
// which still sends tools optimistically (the client falls back per
// session on a 400 tool error and caches the result).
func applyToolsForModel(cfg *config.Config) {
	supported, known := ollama.LookupVision(cfg.ToolsByModel, cfg.Model)
	cfg.ToolsSupported = supported
	cfg.ToolsKnown = known
	logger.Infof("Tools support for model %q: supported=%t known=%t", cfg.Model, supported, known)
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
