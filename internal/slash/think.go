package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// runThinkCommand implements /think: show or set the Ollama thinking effort
// for the active model. Levels persist via /save (THINK in vibe-coder.env).
//
//	/think                 Show the current level and model capability.
//	/think off|low|medium|high|max|on   Set the level for this session.
func runThinkCommand(c *Ctx, args []string) error {
	if len(args) == 0 || strings.ToLower(strings.TrimSpace(args[0])) == "status" {
		printThinkStatus(c)
		return nil
	}
	raw := strings.TrimSpace(args[0])
	norm, ok := ollama.NormalizeThinkLevel(raw)
	if !ok || norm == "" {
		fmt.Fprintf(c.Out, "Invalid think level %q: expected off|low|medium|high|max|on\n", raw)
		return nil
	}
	c.Cfg.OllamaThinkLevel = norm
	if norm == "off" {
		c.Cfg.OllamaNoThink = true
	} else {
		c.Cfg.OllamaNoThink = false
	}
	refreshThinkingFlags(c)
	fmt.Fprintf(c.Out, "Thinking level set to: %s%s. Run /save to persist.\n", norm, thinkCapabilitySuffix(c))
	return nil
}

func printThinkStatus(c *Ctx) {
	refreshThinkingFlags(c)
	level := strings.TrimSpace(c.Cfg.OllamaThinkLevel)
	if level == "" {
		level = "default"
	}
	if c.Cfg.OllamaNoThink && level == "default" {
		level = "off"
	}
	fmt.Fprintf(c.Out, "Thinking level: %s%s\n", level, thinkCapabilitySuffix(c))
	fmt.Fprintln(c.Out, "Usage: /think off|low|medium|high|max|on (levels need a thinking-capable model)")
}

func thinkCapabilitySuffix(c *Ctx) string {
	switch {
	case c.Cfg.ThinkingKnown && c.Cfg.ThinkingSupported:
		return fmt.Sprintf(" (%s supports thinking)", c.Cfg.Model)
	case c.Cfg.ThinkingKnown && !c.Cfg.ThinkingSupported:
		return fmt.Sprintf(" (%s has no thinking capability; levels are ignored)", c.Cfg.Model)
	default:
		return " (model capability unknown)"
	}
}

// refreshThinkingFlags re-resolves the thinking capability after a model or
// level change, using the cached Tags map.
func refreshThinkingFlags(c *Ctx) {
	supported, known := ollama.LookupVision(c.Cfg.ThinkingByModel, c.Cfg.Model)
	c.Cfg.ThinkingSupported = supported
	c.Cfg.ThinkingKnown = known
}

// thinkingStatus renders the /status Thinking line: effective level plus
// what the model actually supports.
func thinkingStatus(c *Ctx) string {
	level := strings.TrimSpace(c.Cfg.OllamaThinkLevel)
	if level == "" {
		level = "default"
	}
	if c.Cfg.OllamaNoThink && level == "default" {
		level = "off"
	}
	switch {
	case c.Cfg.ThinkingKnown && c.Cfg.ThinkingSupported:
		return level + " (supported)"
	case c.Cfg.ThinkingKnown:
		return level + " (not supported)"
	default:
		return level + " (unknown)"
	}
}
