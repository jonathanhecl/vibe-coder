package main

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
	"github.com/jonathanhecl/vibe-coder/internal/version"
)

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
