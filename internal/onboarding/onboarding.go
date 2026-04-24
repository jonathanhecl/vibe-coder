package onboarding

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

const (
	defaultOllamaHost = "http://localhost:11434"
	defaultModel      = "qwen3.5:4b"
)

var ErrInterrupted = errors.New("first-run setup interrupted")

type wizard struct {
	reader *bufio.Reader
	out    io.Writer
	style  tui.Style
}

func RunFirstRun(ctx context.Context, cfg *config.Config, buildVersion string, in io.Reader, out io.Writer) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	w := &wizard{
		reader: bufio.NewReader(in),
		out:    out,
		style:  tui.NewStyle(out),
	}
	sigCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	w.printIntro(buildVersion)
	host, client, models, err := w.selectHostAndModels(sigCtx, cfg.OllamaHost)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return ErrInterrupted
		}
		return err
	}
	cfg.OllamaHost = host

	mainModel, err := w.chooseMainModel(sigCtx, client, models)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return ErrInterrupted
		}
		return err
	}
	cfg.Model = mainModel

	updatedModels, err := client.Tags(sigCtx)
	if err != nil {
		fmt.Fprintf(w.out, "warning: could not refresh model list after selection: %v\n", err)
		updatedModels = models
	}
	sidecarModel, sidecarDisabled, err := w.chooseSidecarModel(sigCtx, client, updatedModels)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return ErrInterrupted
		}
		return err
	}
	cfg.SidecarModel = sidecarModel
	cfg.SidecarDisabled = sidecarDisabled
	cfg.SidecarSkipSession = false

	if err := config.SaveModelSettings(cfg); err != nil {
		return fmt.Errorf("save first-run settings: %w", err)
	}
	w.printFinal(cfg)
	return nil
}

func (w *wizard) printIntro(buildVersion string) {
	header := "vibe-coder " + strings.TrimSpace(buildVersion)
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "\n%s %s\n", w.style.BoldBrightGreen(">>"), w.style.BoldGreen(header))
		fmt.Fprintf(w.out, "%s\n", w.style.DimGreen("Welcome. Let's set up your first run."))
	} else {
		fmt.Fprintf(w.out, "\n%s\n", header)
		fmt.Fprintln(w.out, "Welcome. Let's set up your first run.")
	}
	fmt.Fprintln(w.out, "This assistant works with Ollama or Ollama-compatible hosts.")
}

func (w *wizard) selectHostAndModels(ctx context.Context, currentHost string) (string, ollama.Client, []ollama.Model, error) {
	host := strings.TrimSpace(currentHost)
	if host == "" {
		host = defaultOllamaHost
	}

	for {
		w.section("Host setup")
		w.option("Enter", fmt.Sprintf("Use local Ollama (%s) [default]", defaultOllamaHost))
		w.option("c", "Enter host URL manually")
		choice, err := w.prompt(ctx, "Host choice [Enter/c]: ")
		if err != nil {
			return "", nil, nil, err
		}
		choice = strings.TrimSpace(strings.ToLower(choice))
		switch choice {
		case "":
			host = defaultOllamaHost
		case "c":
			value, err := w.prompt(ctx, fmt.Sprintf("Host URL (example: %s): ", host))
			if err != nil {
				return "", nil, nil, err
			}
			value = strings.TrimSpace(value)
			if value == "" {
				return "", nil, nil, fmt.Errorf("host URL cannot be empty")
			}
			host = value
		default:
			w.warn("Invalid choice. Press Enter for local host, or c for custom URL.")
			continue
		}

		client := ollama.NewHTTP(host)
		versionCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		versionValue, vErr := client.Version(versionCtx)
		cancel()
		if vErr != nil {
			w.warn(fmt.Sprintf("Could not reach host %q: %v", host, vErr))
			if retry, err := w.retryHostPrompt(ctx); err != nil {
				return "", nil, nil, err
			} else if retry {
				continue
			}
			return "", nil, nil, fmt.Errorf("first-run setup cancelled")
		}

		tagsCtx, cancelTags := context.WithTimeout(ctx, 30*time.Second)
		models, tErr := client.Tags(tagsCtx)
		cancelTags()
		if tErr != nil {
			w.warn(fmt.Sprintf("Connected to Ollama %s, but failed to list models: %v", versionValue, tErr))
			if retry, err := w.retryHostPrompt(ctx); err != nil {
				return "", nil, nil, err
			} else if retry {
				continue
			}
			return "", nil, nil, fmt.Errorf("first-run setup cancelled")
		}
		models = w.resolveModelCapabilities(ctx, client, models)

		w.good(fmt.Sprintf("Connected to Ollama %s at %s", versionValue, host))
		return host, client, models, nil
	}
}

func (w *wizard) retryHostPrompt(ctx context.Context) (bool, error) {
	line, err := w.prompt(ctx, "Try a different host? [Y/n]: ")
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(strings.ToLower(line)) {
	case "", "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func (w *wizard) chooseMainModel(ctx context.Context, client ollama.Client, models []ollama.Model) (string, error) {
	for {
		models = w.resolveModelCapabilities(ctx, client, models)
		toolCapable := ollama.FilterToolCapableModels(models)
		w.section("Primary model selection")
		if len(toolCapable) == 0 {
			w.warn("No tool-capable installed models were reported by /api/tags.")
		} else {
			w.label(fmt.Sprintf("Selectable tool-capable models (%d)", len(toolCapable)))
			for i, model := range toolCapable {
				w.option(fmt.Sprintf("%d", i+1), model.Name)
			}
		}
		w.option("Enter", "Use recommended "+defaultModel)
		w.option("c", "Pull and use another model now (no tools validation)")

		choice, err := w.prompt(ctx, "Primary model choice: ")
		if err != nil {
			return "", err
		}
		choice = strings.TrimSpace(strings.ToLower(choice))
		switch {
		case choice == "":
			if hasModel(models, defaultModel) {
				w.good("Using installed recommended model: " + defaultModel)
				w.selected("Model selected: " + defaultModel)
				return defaultModel, nil
			}
			if err := w.pullModel(ctx, client, defaultModel); err != nil {
				w.warn(fmt.Sprintf("Pull failed for %s: %v", defaultModel, err))
				models = w.refreshModels(ctx, client, models)
				continue
			}
			w.selected("Model selected: " + defaultModel)
			return defaultModel, nil
		case choice == "c":
			custom, err := w.prompt(ctx, "Model name to pull and use: ")
			if err != nil {
				return "", err
			}
			custom = strings.TrimSpace(custom)
			if custom == "" {
				w.warn("Model name cannot be empty.")
				continue
			}
			if err := w.pullModel(ctx, client, custom); err != nil {
				w.warn(fmt.Sprintf("Pull failed for %s: %v", custom, err))
				models = w.refreshModels(ctx, client, models)
				continue
			}
			w.selected("Model selected: " + custom)
			return custom, nil
		default:
			idx, ok := parseListIndex(choice, len(toolCapable))
			if !ok {
				w.warn("Invalid choice. Pick a listed number, Enter, or c.")
				continue
			}
			selected := toolCapable[idx].Name
			w.selected("Model selected: " + selected)
			return selected, nil
		}
	}
}

func (w *wizard) chooseSidecarModel(ctx context.Context, client ollama.Client, models []ollama.Model) (string, bool, error) {
	for {
		models = w.resolveModelCapabilities(ctx, client, models)
		toolCapable := ollama.FilterToolCapableModels(models)
		w.section("Sidecar (optional)")
		w.subtle("The sidecar helps summarize long tool outputs and keep context compact.")
		if len(toolCapable) > 0 {
			w.label(fmt.Sprintf("Selectable tool-capable sidecar models (%d)", len(toolCapable)))
			for i, model := range toolCapable {
				w.option(fmt.Sprintf("%d", i+1), model.Name)
			}
		} else {
			w.warn("No tool-capable installed models were reported by /api/tags.")
		}
		w.option("Enter", "Disable sidecar [default]")
		w.option("c", "Pull and use another sidecar model now (no tools validation)")

		choice, err := w.prompt(ctx, "Sidecar choice: ")
		if err != nil {
			return "", false, err
		}
		choice = strings.TrimSpace(strings.ToLower(choice))
		switch {
		case choice == "":
			w.selected("Sidecar disabled")
			return "", true, nil
		case choice == "c":
			custom, err := w.prompt(ctx, "Sidecar model name to pull and use: ")
			if err != nil {
				return "", false, err
			}
			custom = strings.TrimSpace(custom)
			if custom == "" {
				w.warn("Model name cannot be empty.")
				continue
			}
			if err := w.pullModel(ctx, client, custom); err != nil {
				w.warn(fmt.Sprintf("Pull failed for %s: %v", custom, err))
				models = w.refreshModels(ctx, client, models)
				continue
			}
			w.selected("Sidecar selected: " + custom)
			return custom, false, nil
		default:
			idx, ok := parseListIndex(choice, len(toolCapable))
			if !ok {
				w.warn("Invalid choice. Pick a listed number, Enter, or c.")
				continue
			}
			selected := toolCapable[idx].Name
			w.selected("Sidecar selected: " + selected)
			return selected, false, nil
		}
	}
}

func (w *wizard) refreshModels(ctx context.Context, client ollama.Client, fallback []ollama.Model) []ollama.Model {
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	models, err := client.Tags(refreshCtx)
	if err != nil {
		return fallback
	}
	return w.resolveModelCapabilities(ctx, client, models)
}

func (w *wizard) pullModel(ctx context.Context, client ollama.Client, model string) error {
	w.label("Pulling " + model + " ...")
	lastStatus := ""
	pullCtx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer cancel()
	err := client.Pull(pullCtx, model, func(ev ollama.PullEvent) {
		status := strings.TrimSpace(ev.Status)
		if status == "" || status == lastStatus {
			return
		}
		lastStatus = status
		w.subtle("  - " + status)
	})
	if err != nil {
		return err
	}
	w.good("Pull complete: " + model)
	return nil
}

func (w *wizard) printFinal(cfg *config.Config) {
	if w.style.Enabled() {
		fmt.Fprintln(w.out, "")
		fmt.Fprintln(w.out, w.style.BoldBrightGreen("┌────────────────────────────────────────────┐"))
		fmt.Fprintln(w.out, w.style.BoldBrightGreen("│  vibe-coder setup complete                 │"))
		fmt.Fprintln(w.out, w.style.BoldBrightGreen("└────────────────────────────────────────────┘"))
	} else {
		fmt.Fprintln(w.out, "\n--- vibe-coder setup complete ---")
	}
	fmt.Fprintf(w.out, "Saved settings to %s\n", cfg.ConfigFile)
	fmt.Fprintln(w.out)
}

func (w *wizard) resolveModelCapabilities(ctx context.Context, client ollama.Client, models []ollama.Model) []ollama.Model {
	timeoutCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return ollama.ResolveModelCapabilities(timeoutCtx, client, models)
}

func (w *wizard) section(title string) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "\n%s %s\n", w.style.BoldMagenta("◆"), w.style.BoldYellow(title))
		return
	}
	fmt.Fprintf(w.out, "\n%s:\n", title)
}

func (w *wizard) label(text string) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "%s %s\n", w.style.BoldCyan("•"), w.style.BoldCyan(text))
		return
	}
	fmt.Fprintln(w.out, text+":")
}

func (w *wizard) option(key, text string) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "  %s %s\n", w.style.BoldBrightGreen("["+key+"]"), text)
		return
	}
	fmt.Fprintf(w.out, "  [%s] %s\n", key, text)
}

func (w *wizard) subtle(text string) {
	if w.style.Enabled() {
		fmt.Fprintln(w.out, w.style.Dim(text))
		return
	}
	fmt.Fprintln(w.out, text)
}

func (w *wizard) warn(text string) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "%s %s\n", w.style.BoldRed("!"), w.style.Yellow(text))
		return
	}
	fmt.Fprintln(w.out, text)
}

func (w *wizard) good(text string) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "%s %s\n", w.style.BoldGreen("✓"), w.style.BrightGreen(text))
		return
	}
	fmt.Fprintln(w.out, text)
}

func (w *wizard) selected(text string) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "%s %s %s\n", w.style.DimGreen("onboarding"), w.style.DimGreen("›"), w.style.BrightGreen(text))
		return
	}
	fmt.Fprintln(w.out, text)
}

func hasModel(models []ollama.Model, target string) bool {
	target = strings.TrimSpace(target)
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model.Name), target) {
			return true
		}
	}
	return false
}

func parseListIndex(raw string, total int) (int, bool) {
	if total <= 0 {
		return 0, false
	}
	raw = strings.TrimSpace(raw)
	n := 0
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	if n <= 0 || n > total {
		return 0, false
	}
	return n - 1, true
}

func (w *wizard) prompt(ctx context.Context, label string) (string, error) {
	if w.style.Enabled() {
		fmt.Fprintf(w.out, "%s %s", w.style.DimGreen("onboarding"), w.style.BoldBrightGreen("› "+label))
	} else {
		fmt.Fprint(w.out, label)
	}
	type lineResult struct {
		line string
		err  error
	}
	lineCh := make(chan lineResult, 1)
	go func() {
		line, err := w.reader.ReadString('\n')
		lineCh <- lineResult{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		return "", ErrInterrupted
	case res := <-lineCh:
		if res.err != nil {
			return "", res.err
		}
		return strings.TrimRight(strings.TrimRight(res.line, "\n"), "\r"), nil
	}
}
