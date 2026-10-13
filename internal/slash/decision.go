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

func runDecisionCommand(c *Ctx, args []string) error {
	if len(args) == 0 {
		printDecisionStatus(c)
		printDecisionUsage(c)
		return nil
	}
	sub := strings.TrimSpace(args[0])
	switch strings.ToLower(sub) {
	case "status":
		printDecisionStatus(c)
	case "test":
		return runDecisionTest(c)
	case "endpoint":
		if len(args) < 2 {
			fmt.Fprintln(c.Out, "Usage: /decision endpoint <url|ollama>")
			return nil
		}
		url := strings.TrimSpace(args[1])
		if strings.EqualFold(url, "ollama") {
			c.Cfg.DecisionEndpoint = ""
			fmt.Fprintln(c.Out, "Decision endpoint cleared; the Ollama host will be used. Run /save to persist.")
		} else {
			if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
				fmt.Fprintf(c.Out, "Invalid endpoint %q: must start with http:// or https://\n", url)
				return nil
			}
			c.Cfg.DecisionEndpoint = url
			fmt.Fprintf(c.Out, "Decision endpoint set to: %s (run /save to persist)\n", url)
		}
		checkDecision(c)
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
	case "off", "disable", "none":
		c.Cfg.DecisionModel = ""
		c.Cfg.DecisionEndpoint = ""
		c.Cfg.AssistedYes = false
		if c.Perm != nil {
			c.Perm.SetAssistedMode(false)
		}
		fmt.Fprintln(c.Out, "Decision model disabled for this session.")
	default:
		if strings.EqualFold(sub, "set") && len(args) > 1 {
			sub = strings.TrimSpace(args[1])
		} else if len(args) > 1 {
			fmt.Fprintln(c.Out, "Invalid model name format.")
			return nil
		}
		resolved, ok := c.resolveModelArg(context.Background(), sub)
		if !ok {
			fmt.Fprintf(c.Out, "No model number %s in the installed list.\n", sub)
			return nil
		}
		if !modelNameRe.MatchString(resolved) {
			fmt.Fprintln(c.Out, "Invalid model name format.")
			return nil
		}
		c.Cfg.DecisionModel = resolved
		c.Cfg.DecisionEndpoint = ""
		fmt.Fprintf(c.Out, "Decision model set to: %s (run /save to persist)\n", c.Cfg.DecisionModel)
		checkDecision(c)
		if !c.Cfg.AssistedYes {
			fmt.Fprintln(c.Out, "Tip: Enable assisted execution mode with /yes assisted or /decision assisted on (auto-approves safe commands, prompts for dangerous ones).")
		}
	}
	return nil
}

// checkDecision builds a client from the config, runs Check, records the
// backend for /decision status, and prints the outcome.
func checkDecision(c *Ctx) {
	client := decision.FromConfig(c.Cfg)
	if client == nil {
		lastDecisionCheck = nil
		fmt.Fprintln(c.Out, "No decision model configured.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	info, err := client.Check(ctx)
	if err != nil {
		lastDecisionCheck = nil
		fmt.Fprintf(c.Out, "Decision check failed: %v\n", err)
		return
	}
	lastDecisionCheck = &info
	fmt.Fprintf(c.Out, "Decision backend: %s (model: %s)\n", info.Backend, info.Model)
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
		fmt.Fprintln(c.Out, "Decision model: not configured (DECISION_MODEL or DECISION_ENDPOINT).")
		return
	}
	fmt.Fprintf(c.Out, "Decision model: %s | endpoint: %s | backend: %s | assisted mode: %s\n",
		model, endpoint, backend, assisted)
}

func printDecisionUsage(c *Ctx) {
	fmt.Fprintln(c.Out, "Usage:")
	fmt.Fprintln(c.Out, "  /decision <name|number>      set decision model (resolved against the Ollama host)")
	fmt.Fprintln(c.Out, "  /decision endpoint <url>     use a custom System One server endpoint")
	fmt.Fprintln(c.Out, "  /decision endpoint ollama    use the Ollama host (native System One)")
	fmt.Fprintln(c.Out, "  /decision test               run a connectivity and decision test")
	fmt.Fprintln(c.Out, "  /decision assisted on|off    toggle assisted execution mode")
	fmt.Fprintln(c.Out, "  /decision off                disable the decision model for this session")
}

func runDecisionTest(c *Ctx) error {
	client := decision.FromConfig(c.Cfg)
	if client == nil {
		fmt.Fprintln(c.Out, "Cannot test: no decision model configured. Use /decision <model> or /decision endpoint <url>.")
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
