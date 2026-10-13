package onboarding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// chooseMainModel lists the installed models once and asks for the primary
// model. It is the only model the first run configures: the sidecar, decision,
// embedding and vision roles are bound later from inside vibe with /setmodel.
func (w *wizard) chooseMainModel(ctx context.Context, client ollama.Client, models []ollama.Model) (string, error) {
	recommended := recommendedModel()

	w.section(2, 2, "Main model")
	w.printModelList(models, recommended)
	w.option("Enter", "Use recommended "+recommended, "main")
	w.option("c", "Pull and use another model now", "no tools validation")
	w.hint("Enter a number from the list, or press Enter for the default.")
	w.hint("Bind the other roles later with /providers, /models and /setmodel.")

	return w.choosePrimaryModel(ctx, client, models, recommended)
}

// printModelList renders the numbered list of installed models. The capability
// tags let the user pick a tool-capable main model without a second listing.
func (w *wizard) printModelList(models []ollama.Model, recommended string) {
	if len(models) == 0 {
		w.warn("No installed models were reported by /api/tags.")
		return
	}
	w.label(fmt.Sprintf("Installed models (%d)", len(models)))
	for i, model := range models {
		w.option(fmt.Sprintf("%d", i+1), model.Name, modelTags(model, recommended)...)
	}
}

func modelTags(model ollama.Model, recommended string) []string {
	var tags []string
	if strings.EqualFold(strings.TrimSpace(model.Name), recommended) {
		tags = append(tags, "recommended")
	}
	if model.SupportsTools() {
		tags = append(tags, "tools")
	}
	if model.SupportsVision() {
		tags = append(tags, "vision")
	}
	if model.SupportsThinking() {
		tags = append(tags, "thinking")
	}
	return tags
}

// choosePrimaryModel resolves the required main model. Enter selects the
// recommended default, c pulls a custom model, and a list number must point at
// a tool-capable model (when `/api/show` reported capabilities).
func (w *wizard) choosePrimaryModel(ctx context.Context, client ollama.Client, models []ollama.Model, recommended string) (string, error) {
	for {
		choice, err := w.prompt(ctx, "Main model choice: ")
		if err != nil {
			return "", err
		}
		switch choice = strings.TrimSpace(strings.ToLower(choice)); {
		case choice == "":
			if hasModel(models, recommended) {
				w.selected("Using installed recommended model: " + recommended)
				return recommended, nil
			}
			if err := w.pullModel(ctx, client, recommended); err != nil {
				w.warn(fmt.Sprintf("Pull failed for %s: %v", recommended, err))
				continue
			}
			w.selected("Main model selected: " + recommended)
			return recommended, nil
		case choice == "c":
			custom, err := w.promptModelName(ctx, "Model name to pull and use: ")
			if err != nil {
				return "", err
			}
			if err := w.pullModel(ctx, client, custom); err != nil {
				w.warn(fmt.Sprintf("Pull failed for %s: %v", custom, err))
				continue
			}
			w.selected("Main model selected: " + custom)
			return custom, nil
		default:
			idx, ok := parseListIndex(choice, len(models))
			if !ok {
				w.warn("Invalid choice. Pick a listed number, Enter, or c.")
				continue
			}
			selected := models[idx]
			if selected.CapabilitiesKnown && !selected.SupportsTools() {
				w.warn(fmt.Sprintf("%s does not report tool support; pick another model for the main role.", selected.Name))
				continue
			}
			w.selected("Main model selected: " + selected.Name)
			return selected.Name, nil
		}
	}
}

// promptModelName reads a non-empty model name for the custom-pull path.
func (w *wizard) promptModelName(ctx context.Context, label string) (string, error) {
	for {
		name, err := w.prompt(ctx, label)
		if err != nil {
			return "", err
		}
		if name = strings.TrimSpace(name); name != "" {
			return name, nil
		}
		w.warn("Model name cannot be empty.")
	}
}

func (w *wizard) pullModel(ctx context.Context, client ollama.Client, model string) error {
	w.label("Pulling " + model + " ...")
	lastStatus := ""
	interactive := w.style.Enabled()
	pullCtx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer cancel()
	err := client.Pull(pullCtx, model, func(ev ollama.PullEvent) {
		if ev.Error != "" {
			return
		}
		if interactive && ev.Total > 0 {
			w.pullProgress(model, ev)
			return
		}
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
	if interactive {
		fmt.Fprint(w.out, w.style.ClearPendingLine())
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
