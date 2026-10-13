package slash

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/decision"
)

// lastDecisionCheck records the most recent decision Check result so /decision
// can report the detected backend without probing again.
var lastDecisionCheck *decision.Info

// runDecisionCommand carries only the decision behavior that is not model
// configuration: the backend status, a connectivity test, and the assisted
// execution toggle. The decision model and its endpoint are bound with
// /setmodel decision <number|name>.
func runDecisionCommand(c *Ctx, args []string) error {
	if len(args) == 0 {
		printDecisionStatus(c)
		printDecisionUsage(c)
		return nil
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	switch sub {
	case "status":
		printDecisionStatus(c)
	case "test":
		return runDecisionTest(c)
	case "assisted":
		if len(args) > 1 {
			switch strings.ToLower(strings.TrimSpace(args[1])) {
			case "on", "true", "yes", "1", "enable":
				c.Cfg.AssistedYes = true
				if c.Perm != nil {
					c.Perm.SetAssistedMode(true)
				}
				fmt.Fprintln(c.Out, "Decision assisted mode enabled.")
			case "off", "false", "no", "0", "disable":
				c.Cfg.AssistedYes = false
				if c.Perm != nil {
					c.Perm.SetAssistedMode(false)
				}
				fmt.Fprintln(c.Out, "Decision assisted mode disabled.")
			default:
				fmt.Fprintln(c.Out, "Usage: /decision assisted on|off")
			}
		} else {
			status := "off"
			if c.Cfg.AssistedYes {
				status = "on"
			}
			fmt.Fprintf(c.Out, "Decision assisted execution mode: %s\n", status)
		}
	case "endpoint", "url", "off", "disable", "none", "set":
		printDecisionModelHint(c)
	default:
		fmt.Fprintf(c.Out, "Unknown /decision action %q.\n", sub)
		printDecisionUsage(c)
	}
	return nil
}

// printDecisionModelHint points at the command that replaced the removed
// model and endpoint routes, so an old habit lands somewhere useful.
func printDecisionModelHint(c *Ctx) {
	fmt.Fprintln(c.Out, "The decision model and endpoint are bound with the provider commands now.")
	fmt.Fprintln(c.Out, "Use /setmodel decision <number|name> (see /models), or /setmodel decision off to unbind it.")
}

func printDecisionStatus(c *Ctx) {
	assisted := "off"
	if c.Cfg.AssistedYes {
		assisted = "on"
	}
	backend := "not checked"
	if lastDecisionCheck != nil {
		backend = lastDecisionCheck.Backend
	}
	endpoint := strings.TrimSpace(c.Cfg.DecisionEndpoint)
	if endpoint == "" {
		endpoint = "(Ollama host)"
	}
	model := strings.TrimSpace(c.Cfg.DecisionModel)
	if model == "" {
		model = "(server default)"
	}
	if !c.Cfg.DecisionInUse() {
		fmt.Fprintln(c.Out, "Decision model: not configured. Bind it with /setmodel decision <number|name>.")
		return
	}
	fmt.Fprintf(c.Out, "Decision model: %s | endpoint: %s | backend: %s | assisted mode: %s\n",
		model, endpoint, backend, assisted)
}

func printDecisionUsage(c *Ctx) {
	fmt.Fprintln(c.Out, "Usage:")
	fmt.Fprintln(c.Out, "  /decision status             show the decision backend and assisted mode")
	fmt.Fprintln(c.Out, "  /decision test               run a connectivity and decision test")
	fmt.Fprintln(c.Out, "  /decision assisted on|off    toggle assisted execution mode")
	fmt.Fprintln(c.Out, "Bind the decision model with /setmodel decision <number|name> (see /models).")
}

func runDecisionTest(c *Ctx) error {
	client := decision.FromConfig(c.Cfg)
	if client == nil {
		fmt.Fprintln(c.Out, "Cannot test: no decision model configured. Bind it with /setmodel decision <number|name>.")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	info, err := client.Check(ctx)
	if err != nil {
		lastDecisionCheck = nil
		fmt.Fprintf(c.Out, "Decision check failed: %v\n", err)
		return nil
	}
	lastDecisionCheck = &info
	fmt.Fprintf(c.Out, "Backend: %s (model: %s, %v)\n", info.Backend, info.Model, time.Since(start).Round(time.Millisecond))

	start = time.Now()
	safety, err := client.AskSafety(ctx, "Bash", "ls -la")
	if err != nil {
		fmt.Fprintf(c.Out, "AskSafety failed: %v\n", err)
		return nil
	}
	fmt.Fprintf(c.Out, "AskSafety (ls -la): action=%s risk=%.1f confidence=%.2f (%v)\n",
		safety.Action, safety.RiskScore, safety.Confidence, time.Since(start).Round(time.Millisecond))

	start = time.Now()
	isRaining, err := client.DecideBool(ctx,
		"The sky is clear and the sun is shining brightly.",
		"Is it currently raining?")
	if err != nil {
		fmt.Fprintf(c.Out, "DecideBool failed: %v\n", err)
		return nil
	}
	fmt.Fprintf(c.Out, "DecideBool (is raining?): %v (expected: false, %v)\n",
		isRaining, time.Since(start).Round(time.Millisecond))

	fmt.Fprintln(c.Out, "Decision test complete.")
	return nil
}
