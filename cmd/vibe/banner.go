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
	hostLabel := "Ollama"
	if cfg != nil {
		model = cfg.Model
		host = cfg.EffectiveModelEndpoint()
		if cfg.IsModelOpenAI() {
			hostLabel = "Endpoint"
		}
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
		{Label: hostLabel, Value: host},
	}
	if cfg != nil && cfg.DecisionInUse() {
		val := decisionBannerValue(cfg)
		if cfg.AssistedYes {
			val = fmt.Sprintf("%s (assisted enabled)", val)
		} else {
			val = fmt.Sprintf("%s (run '/yes assisted' to enable)", val)
		}
		fields = append(fields, bannerField{Label: "Decision", Value: val})
	}
	if cfg != nil && cfg.Offline {
		fields = append(fields, bannerField{Label: "Offline", Value: "enabled (network blocked)"})
	}
	return fields
}

// decisionBannerValue returns the configured decision model target: the
// endpoint URL when one is set, otherwise the decision model name.
func decisionBannerValue(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if ep := strings.TrimSpace(cfg.DecisionEndpoint); ep != "" {
		return ep
	}
	return strings.TrimSpace(cfg.DecisionModel)
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
		if cfg != nil && cfg.DecisionInUse() {
			decisionVal := decisionBannerValue(cfg)
			if cfg.AssistedYes {
				decisionVal = fmt.Sprintf("%s (assisted enabled)", decisionVal)
			} else {
				decisionVal = fmt.Sprintf("%s (run '/yes assisted' to enable)", decisionVal)
			}
			out += fmt.Sprintf("Decision: %s\n", decisionVal)
		}
		if cfg != nil && cfg.Offline {
			out += "Offline: enabled (network blocked)\n"
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
		} else if f.Label == "Decision" {
			if cfg != nil && cfg.AssistedYes {
				b.WriteString(style.BrightWhite(decisionBannerValue(cfg)))
				b.WriteString(" ")
				b.WriteString(style.Green("(assisted enabled)"))
			} else if cfg != nil && cfg.DecisionInUse() {
				b.WriteString(style.BrightWhite(decisionBannerValue(cfg)))
				b.WriteString(" ")
				b.WriteString(style.BrightWhite("(run '/yes assisted' to enable)"))
			} else {
				b.WriteString(style.BrightWhite(f.Value))
			}
		} else if f.Label == "Offline" {
			b.WriteString(style.Yellow(f.Value))
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
