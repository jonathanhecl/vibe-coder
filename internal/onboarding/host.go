package onboarding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

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
