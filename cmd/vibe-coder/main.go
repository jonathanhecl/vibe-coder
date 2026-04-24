package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/agent"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/mcp"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/onboarding"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/slash"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
	"github.com/jonathanhecl/vibe-coder/internal/version"
	"github.com/jonathanhecl/vibe-coder/internal/watcher"
	"golang.org/x/term"
)

const maxExternalEmptyRetries = 3

func main() {
	args, persistModelSettings := extractPersistDirective(os.Args[1:])
	cfg, err := config.Load(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if persistModelSettings {
		cfg.PersistSidecarOffFromSave(true)
		if err := config.SaveModelSettings(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to save model settings: %v\n", err)
			os.Exit(1)
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
			fmt.Fprintf(os.Stderr, "error: first-run setup failed: %v\n", err)
			os.Exit(1)
		}
	}

	client := ollama.NewHTTP(cfg.OllamaHost)
	sess := session.New(cfg)
	sess.SetClient(client)
	ui := tui.NewPlain()
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
	ag.SetWatcher(watcher.New(cfg.Cwd))
	defer ui.Stop()

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	installSignalHandler(ui, sess, rootCancel)

	ragHandled, ragMsg, ragErr := configureRAG(rootCtx, cfg, client, ag)
	if ragErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", ragErr)
		os.Exit(1)
	}
	if ragHandled {
		if strings.TrimSpace(ragMsg) != "" {
			fmt.Fprintln(os.Stdout, ragMsg)
		}
		return
	}

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

	bannerPrinted := false
	if cfg.Prompt != "" {
		// Keep one-shot output aligned with interactive startup context so users
		// can always see which model/session/host served the answer.
		fmt.Fprint(os.Stdout, startupBanner(cfg, sess.ID(), tui.NewStyle(os.Stdout)))
		bannerPrinted = true
		if err := runAgentWithEmptyRetry(rootCtx, ag, ui, cfg.Prompt); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := sess.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to save session: %v\n", err)
			os.Exit(1)
		}
		if !shouldContinueInteractiveAfterPrompt(cfg, term.IsTerminal(int(os.Stdin.Fd())), term.IsTerminal(int(os.Stdout.Fd()))) {
			return
		}
		// Banner was already printed for the initial prompt.
		cfg.Prompt = ""
	}

	if !bannerPrinted {
		fmt.Fprint(os.Stdout, startupBanner(cfg, sess.ID(), tui.NewStyle(os.Stdout)))
	}
	slashCtx := &slash.Ctx{
		Cfg:     cfg,
		Session: sess,
		Perm:    perm,
		Agent:   ag,
		Client:  client,
		Out:     os.Stdout,
	}

	for {
		ui.SetPlanMode(ag.InPlanMode())
		line, err := ui.GetInput("> ")
		if err != nil {
			if err := sess.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "error: failed to save session: %v\n", err)
			}
			printByeOnInterrupt()
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
			ui.SetPlanMode(ag.InPlanMode())
			if task, ok := planTaskFromSlash(line); ok {
				if err := runAgentWithEmptyRetry(rootCtx, ag, ui, task); err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
				}
				ui.SetPlanMode(ag.InPlanMode())
			}
			continue
		}

		ui.SetPlanMode(ag.InPlanMode())
		if err := runAgentWithEmptyRetry(rootCtx, ag, ui, line); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
	}
}

func runAgentWithEmptyRetry(rootCtx context.Context, ag *agent.Agent, ui *tui.PlainUI, input string) error {
	retryCount := 0
	repeatedState := false
	currentInput := strings.TrimSpace(input)
	for {
		err := ag.Run(rootCtx, currentInput)
		if err == nil {
			return nil
		}
		if !isEmptyAssistantResponseErrText(err) {
			return err
		}
		if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
			return err
		}
		retryCount++
		repeatedState = retryCount > 1
		if retryCount >= maxExternalEmptyRetries {
			return fmt.Errorf("empty assistant response persisted after %d retries; pending TODO state may be stuck", retryCount)
		}
		ans, askErr := ui.GetInput("Model returned an empty response. Retry this step? [Y/n]: ")
		if askErr != nil {
			return err
		}
		a := strings.ToLower(strings.TrimSpace(ans))
		if a == "" || a == "y" || a == "yes" {
			currentInput = ag.BuildEmptyResponseRetryInput(input, repeatedState)
			continue
		}
		return nil
	}
}

func isEmptyAssistantResponseErrText(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "empty assistant response")
}

func shouldRunFirstRunOnboarding(cfg *config.Config, persistModelSettings bool) bool {
	if cfg == nil || persistModelSettings {
		return false
	}
	if cfg.ConfigFileExists {
		return false
	}
	if cfg.Prompt != "" || cfg.ListSessions || cfg.Resume {
		return false
	}
	if strings.TrimSpace(cfg.RAGIndex) != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func shouldContinueInteractiveAfterPrompt(cfg *config.Config, stdinTTY, stdoutTTY bool) bool {
	if cfg == nil || strings.TrimSpace(cfg.Prompt) == "" || !cfg.Interactive {
		return false
	}
	return stdinTTY && stdoutTTY
}

func startupBanner(cfg *config.Config, sessionID string, style tui.Style) string {
	sidecar := formatSidecarBanner(cfg)
	if !style.Enabled() {
		return fmt.Sprintf(
			"vibe-coder %s\nSession started: %s\nModel: %s\nSidecar: %s\nOllama host: %s\n",
			version.Value, sessionID, cfg.Model, sidecar, cfg.OllamaHost,
		)
	}
	label := func(k, v string) string {
		return fmt.Sprintf("%s %s\n", style.BoldGreen(k+":"), style.BrightGreen(v))
	}
	header := fmt.Sprintf("%s %s\n",
		style.BoldGreen("vibe-coder"),
		style.DimGreen(version.Value),
	)
	return header +
		label("Session started", sessionID) +
		label("Model", cfg.Model) +
		label("Sidecar", sidecar) +
		label("Ollama host", cfg.OllamaHost)
}

func formatSidecarBanner(cfg *config.Config) string {
	if cfg == nil {
		return "(disabled)"
	}
	if cfg.SidecarDisabled {
		return "(disabled)"
	}
	if cfg.SidecarSkipSession {
		m := strings.TrimSpace(cfg.SidecarModel)
		if m == "" {
			return "(session off — no SIDECAR_MODEL)"
		}
		return fmt.Sprintf("%s (session off — /sidecar on)", m)
	}
	m := strings.TrimSpace(cfg.SidecarModel)
	if m == "" {
		return "(disabled — set SIDECAR_MODEL)"
	}
	return m
}

// installSignalHandler ensures the terminal is always restored on exit, so a
// Ctrl+C never leaves PowerShell or any TTY in raw mode (no echo, BackSpace
// rendered as ^H, etc.). The first signal cancels in-flight work, restores the
// terminal, persists the session, and then forces a clean exit shortly after
// to avoid blocked stdin reads after cancellation.
func installSignalHandler(ui interface{ Stop() }, sess interface{ Save() error }, cancel context.CancelFunc) {
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		if ui != nil {
			ui.Stop()
		}
		if cancel != nil {
			cancel()
		}
		if sess != nil {
			_ = sess.Save()
		}
		printByeOnInterrupt()

		go func() {
			<-sigCh
			if ui != nil {
				ui.Stop()
			}
			os.Exit(130)
		}()

		time.Sleep(400 * time.Millisecond)
		if ui != nil {
			ui.Stop()
		}
		os.Exit(130)
	}()
}

// printByeOnInterrupt prints the goodbye line at most once. Both the signal
// handler and the read loop (stdin closed / interrupted) can run on Ctrl+C.
var byeOnInterruptOnce sync.Once

func printByeOnInterrupt() {
	byeOnInterruptOnce.Do(func() {
		fmt.Fprintln(os.Stdout, "\nBye.")
	})
}

func extractPersistDirective(args []string) ([]string, bool) {
	filtered := make([]string, 0, len(args))
	persist := false
	for _, arg := range args {
		if strings.EqualFold(strings.TrimSpace(arg), "/save") {
			persist = true
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered, persist
}

// planTaskFromSlash extracts an immediate planning goal from "/plan <goal>".
// Control forms like "/plan off|exit|cancel" do not return a task.
func planTaskFromSlash(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "/plan") {
		return "", false
	}
	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(fields[1])) {
	case "off", "exit", "cancel":
		return "", false
	}
	task := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))
	if task == "" {
		return "", false
	}
	return task, true
}
