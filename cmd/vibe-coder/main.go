package main

import (
	"context"
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
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/slash"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
	"github.com/jonathanhecl/vibe-coder/internal/version"
	"github.com/jonathanhecl/vibe-coder/internal/watcher"
)

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

	if cfg.Prompt != "" {
		// Keep one-shot output aligned with interactive startup context so users
		// can always see which model/session/host served the answer.
		fmt.Fprint(os.Stdout, startupBanner(cfg, sess.ID(), tui.NewStyle(os.Stdout)))
		if err := ag.Run(rootCtx, cfg.Prompt); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := sess.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to save session: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Fprint(os.Stdout, startupBanner(cfg, sess.ID(), tui.NewStyle(os.Stdout)))
	slashCtx := &slash.Ctx{
		Cfg:     cfg,
		Session: sess,
		Perm:    perm,
		Agent:   ag,
		Client:  client,
		Out:     os.Stdout,
	}

	for {
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
			continue
		}

		if err := ag.Run(rootCtx, line); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
	}
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
		return "(off in config — remove SIDECAR_DISABLED or set SIDECAR_ENABLED=true)"
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
