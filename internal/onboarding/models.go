package onboarding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// modelSelection holds the models chosen in the single model-selection step.
type modelSelection struct {
	Main            string
	Sidecar         string
	SidecarDisabled bool
	Jevstyle        string
}

// chooseModels lists the installed models once and then asks for the primary,
// optional sidecar, and optional JEV Style models, reusing the same list
// numbers instead of printing the list again for each role.
func (w *wizard) chooseModels(ctx context.Context, client ollama.Client, models []ollama.Model) (modelSelection, error) {
	var sel modelSelection

	w.section(2, 2, "Model selection")
	w.printModelList(models)
	w.option("Enter", "Use recommended "+defaultModel, "primary only")
	w.option("c", "Pull and use another model now", "no tools validation")
	w.hint("Enter a number from the list, or press Enter for the default.")

	main, err := w.choosePrimaryModel(ctx, client, models)
	if err != nil {
		return sel, err
	}
	sel.Main = main

	sidecar, sidecarDisabled, err := w.chooseOptionalModel(ctx, client, models,
		"Sidecar model choice [Enter to disable]: ", "Sidecar selected: ", "Sidecar disabled")
	if err != nil {
		return sel, err
	}
	sel.Sidecar = sidecar
	sel.SidecarDisabled = sidecarDisabled

	jevstyle, _, err := w.chooseOptionalModel(ctx, client, models,
		"JEV Style model choice [Enter to disable]: ", "JEV Style selected: ", "JEV Style disabled")
	if err != nil {
		return sel, err
	}
	sel.Jevstyle = jevstyle

	return sel, nil
}

// printModelList renders the single numbered list shared by every role. The
// capability tags let the user pick a tool-capable primary and an optional
// sidecar/JEV Style model without a second listing.
func (w *wizard) printModelList(models []ollama.Model) {
	if len(models) == 0 {
		w.warn("No installed models were reported by /api/tags.")
		return
	}
	w.label(fmt.Sprintf("Installed models (%d)", len(models)))
	for i, model := range models {
		w.option(fmt.Sprintf("%d", i+1), model.Name, modelTags(model)...)
	}
}

func modelTags(model ollama.Model) []string {
	var tags []string
	if strings.EqualFold(strings.TrimSpace(model.Name), defaultModel) {
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

// choosePrimaryModel resolves the required primary model. Enter selects the
// recommended default, c pulls a custom model, and a list number must point at
// a tool-capable model (when `/api/show` reported capabilities).
func (w *wizard) choosePrimaryModel(ctx context.Context, client ollama.Client, models []ollama.Model) (string, error) {
	for {
		choice, err := w.prompt(ctx, "Primary model choice: ")
		if err != nil {
			return "", err
		}
		switch choice = strings.TrimSpace(strings.ToLower(choice)); {
		case choice == "":
			if hasModel(models, defaultModel) {
				w.selected("Using installed recommended model: " + defaultModel)
				return defaultModel, nil
			}
			if err := w.pullModel(ctx, client, defaultModel); err != nil {
				w.warn(fmt.Sprintf("Pull failed for %s: %v", defaultModel, err))
				continue
			}
			w.selected("Primary model selected: " + defaultModel)
			return defaultModel, nil
		case choice == "c":
			custom, err := w.promptModelName(ctx, "Model name to pull and use: ")
			if err != nil {
				return "", err
			}
			if err := w.pullModel(ctx, client, custom); err != nil {
				w.warn(fmt.Sprintf("Pull failed for %s: %v", custom, err))
				continue
			}
			w.selected("Primary model selected: " + custom)
			return custom, nil
		default:
			idx, ok := parseListIndex(choice, len(models))
			if !ok {
				w.warn("Invalid choice. Pick a listed number, Enter, or c.")
				continue
			}
			selected := models[idx]
			if selected.CapabilitiesKnown && !selected.SupportsTools() {
				w.warn(fmt.Sprintf("%s does not report tool support; pick another model for the primary role.", selected.Name))
				continue
			}
			w.selected("Primary model selected: " + selected.Name)
			return selected.Name, nil
		}
	}
}

// chooseOptionalModel resolves an optional model (sidecar or JEV Style). Enter
// leaves it disabled, c pulls a custom model, and a list number selects any
// installed model. It returns the chosen name and whether the role is disabled.
func (w *wizard) chooseOptionalModel(ctx context.Context, client ollama.Client, models []ollama.Model, promptLabel, selectedLabel, disabledLabel string) (string, bool, error) {
	for {
		choice, err := w.prompt(ctx, promptLabel)
		if err != nil {
			return "", false, err
		}
		switch choice = strings.TrimSpace(strings.ToLower(choice)); {
		case choice == "":
			w.selected(disabledLabel)
			return "", true, nil
		case choice == "c":
			custom, err := w.promptModelName(ctx, "Model name to pull and use: ")
			if err != nil {
				return "", false, err
			}
			if err := w.pullModel(ctx, client, custom); err != nil {
				w.warn(fmt.Sprintf("Pull failed for %s: %v", custom, err))
				continue
			}
			w.selected(selectedLabel + custom)
			return custom, false, nil
		default:
			idx, ok := parseListIndex(choice, len(models))
			if !ok {
				w.warn("Invalid choice. Pick a listed number, Enter, or c.")
				continue
			}
			selected := models[idx].Name
			w.selected(selectedLabel + selected)
			return selected, false, nil
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
