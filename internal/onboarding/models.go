package onboarding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

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
