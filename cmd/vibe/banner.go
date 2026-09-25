package main

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
	"github.com/jonathanhecl/vibe-coder/internal/version"
)

// bannerWordmark is the styled startup logo. It is only emitted when ANSI
// styling is available, so piped or NO_COLOR output stays plain and greppable.
const bannerWordmark = `  ██╗   ██╗██╗██████╗ ███████╗
  ██║   ██║██║██╔══██╗██╔════╝
  ██║   ██║██║██████╔╝█████╗
  ╚██╗ ██╔╝██║██╔══██╗██╔══╝
   ╚████╔╝ ██║██████╔╝███████╗
    ╚═══╝  ╚═╝╚═════╝ ╚══════╝`

// bannerField is one label/value pair shown under the startup logo.
type bannerField struct {
	Label string
	Value string
}

// bannerFields returns the runtime facts surfaced at startup. Keeping them as
// data makes the layout testable without depending on ANSI escape codes.
func bannerFields(cfg *config.Config, sessionID string, resumed bool) []bannerField {
	model := ""
	host := ""
	if cfg != nil {
		model = cfg.Model
		host = cfg.OllamaHost
	}
	sessVal := sessionID
	if cfg != nil && cfg.Temporal {
		sessVal = fmt.Sprintf("%s    (temporal)", sessionID)
	} else if cfg != nil && cfg.Isolated {
		sessVal = fmt.Sprintf("%s    (isolated)", sessionID)
	} else if resumed {
		sessVal = fmt.Sprintf("%s    (resumed)", sessionID)
	}
	fields := []bannerField{
		{Label: "Session", Value: sessVal},
		{Label: "Model", Value: model},
		{Label: "Sidecar", Value: formatSidecarBanner(cfg)},
		{Label: "Ollama", Value: host},
	}
	if cfg != nil && strings.TrimSpace(cfg.JevstyleModel) != "" {
		fields = append(fields, bannerField{Label: "Jevstyle", Value: strings.TrimSpace(cfg.JevstyleModel)})
	}
	return fields
}

func startupBanner(cfg *config.Config, sessionID string, resumed bool, style tui.Style) string {
	if !style.Enabled() {
		sessionLine := "Session started: " + sessionID
		if cfg != nil && cfg.Temporal {
			sessionLine += " (temporal)"
		} else if cfg != nil && cfg.Isolated {
			sessionLine += " (isolated)"
		} else if resumed {
			sessionLine += " (resumed)"
		}
		out := fmt.Sprintf(
			"vibe %s\n%s\nModel: %s\nSidecar: %s\nOllama host: %s\n",
			version.Value, sessionLine, cfg.Model, formatSidecarBanner(cfg), cfg.OllamaHost,
		)
		if cfg != nil && strings.TrimSpace(cfg.JevstyleModel) != "" {
			out += fmt.Sprintf("JEV Style: %s\n", strings.TrimSpace(cfg.JevstyleModel))
		}
		return out
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(style.Cyan(bannerWordmark))
	b.WriteString("\n\n")
	b.WriteString("  ")
	b.WriteString(style.DimGreen("local-first coding agent for Ollama"))
	b.WriteString("  ")
	b.WriteString(style.Gray("· " + version.Value))
	b.WriteString("\n  ")
	b.WriteString(style.Gray(strings.Repeat("─", 46)))
	b.WriteString("\n")
	for _, f := range bannerFields(cfg, sessionID, resumed) {
		b.WriteString("  ")
		b.WriteString(style.BoldCyan(fmt.Sprintf("%-8s", f.Label)))
		b.WriteString(" ")
		if f.Label == "Session" {
			if cfg != nil && cfg.Temporal {
				b.WriteString(style.BrightWhite(sessionID))
				b.WriteString("    ")
				b.WriteString(style.Red("(temporal)"))
			} else if cfg != nil && cfg.Isolated {
				b.WriteString(style.BrightWhite(sessionID))
				b.WriteString("    ")
				b.WriteString(style.Yellow("(isolated)"))
			} else if resumed {
				b.WriteString(style.BrightWhite(sessionID))
				b.WriteString("    ")
				b.WriteString(style.Cyan("(resumed)"))
			} else {
				b.WriteString(style.BrightWhite(f.Value))
			}
		} else {
			b.WriteString(style.BrightWhite(f.Value))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
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
