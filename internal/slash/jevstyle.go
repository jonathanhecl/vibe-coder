package slash

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/jevstyle"
	"github.com/jonathanhecl/vibe-coder/internal/jevstylev3"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

func runJevstyleCommand(c *Ctx, args []string) error {
	if len(args) == 0 {
		printJevstyleStatus(c)
		return c.pickJevstyleMode()
	}
	sub := strings.TrimSpace(args[0])
	switch strings.ToLower(sub) {
	case "status":
		printJevstyleStatus(c)
	case "test":
		return runJevstyleTest(c)
	case "assisted":
		if len(args) > 1 {
			switch strings.ToLower(strings.TrimSpace(args[1])) {
			case "on", "true", "yes", "1", "enable":
				c.Cfg.AssistedYes = true
				if c.Perm != nil {
					c.Perm.SetAssistedMode(true)
				}
				fmt.Fprintln(c.Out, "JEV Style assisted mode enabled.")
			case "off", "false", "no", "0", "disable":
				c.Cfg.AssistedYes = false
				if c.Perm != nil {
					c.Perm.SetAssistedMode(false)
				}
				fmt.Fprintln(c.Out, "JEV Style assisted mode disabled.")
			default:
				fmt.Fprintln(c.Out, "Usage: /jevstyle assisted on|off")
			}
		} else {
			status := "off"
			if c.Cfg.AssistedYes {
				status = "on"
			}
			fmt.Fprintf(c.Out, "JEV Style assisted execution mode: %s\n", status)
		}
	case "v3":
		return c.configureV3()
	case "ollama", "v1", "v2":
		c.Cfg.JevstyleV3Endpoint = ""
		fmt.Fprintln(c.Out, "JEV Style mode set to v1/v2 (Ollama). Run /save to persist.")
	case "off", "disable", "none":
		c.Cfg.JevstyleModel = ""
		c.Cfg.JevstyleV3Endpoint = ""
		c.Cfg.AssistedYes = false
		if c.Perm != nil {
			c.Perm.SetAssistedMode(false)
		}
		fmt.Fprintln(c.Out, "JEV Style disabled for this session.")
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
		c.Cfg.JevstyleModel = resolved
		c.Cfg.JevstyleV3Endpoint = ""
		fmt.Fprintf(c.Out, "JEV Style model set to: %s (run /save to persist)\n", c.Cfg.JevstyleModel)
		if !c.Cfg.AssistedYes {
			fmt.Fprintln(c.Out, "Tip: Enable assisted execution mode with /yes assisted or /jevstyle assisted on (auto-approves safe commands, prompts for dangerous ones).")
		}
	}
	return nil
}

// pickJevstyleMode asks the user to choose between v1/v2 (Ollama) and v3 (own endpoint).
func (c *Ctx) pickJevstyleMode() error {
	if c.Prompter == nil {
		fmt.Fprintln(c.Out, "JEV Style mode:")
		fmt.Fprintln(c.Out, "  [1] JEV Style v1/v2 (Ollama) — text prompt, letter response")
		fmt.Fprintln(c.Out, "  [2] JEV Style v3 (own endpoint) — JSON structured I/O")
		fmt.Fprintln(c.Out, "")
		fmt.Fprintln(c.Out, "Use /jevstyle ollama or /jevstyle v3 to switch modes.")
		return nil
	}

	fmt.Fprintln(c.Out, "JEV Style mode:")
	fmt.Fprintln(c.Out, "  [1] JEV Style v1/v2 (Ollama) — text prompt, letter response")
	fmt.Fprintln(c.Out, "  [2] JEV Style v3 (own endpoint) — JSON structured I/O")
	fmt.Fprintln(c.Out, "")

	choice, err := c.Prompter.GetInput("Select mode [1/2] (Enter to keep current): ")
	if err != nil {
		return nil
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return nil
	}

	switch choice {
	case "1", "ollama", "v1", "v2":
		c.Cfg.JevstyleV3Endpoint = ""
		fmt.Fprintln(c.Out, "JEV Style mode set to v1/v2 (Ollama). Run /save to persist.")
	case "2", "v3":
		return c.configureV3()
	default:
		fmt.Fprintln(c.Out, "Invalid choice. Use /jevstyle ollama or /jevstyle v3.")
	}
	return nil
}

// configureV3 prompts for a v3 endpoint and validates the connection.
func (c *Ctx) configureV3() error {
	if c.Prompter == nil {
		fmt.Fprintln(c.Out, "Usage: /jevstyle v3 <endpoint-url>")
		fmt.Fprintln(c.Out, "Example: /jevstyle v3 http://192.168.0.33:8765")
		return nil
	}

	endpoint, err := c.Prompter.GetInput("Enter JEV v3 endpoint (e.g. http://192.168.0.33:8765): ")
	if err != nil {
		return nil
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		fmt.Fprintln(c.Out, "Cancelled.")
		return nil
	}

	// Validate URL format
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		fmt.Fprintf(c.Out, "Invalid endpoint %q: must start with http:// or https://\n", endpoint)
		return c.retryV3Config()
	}

	client := jevstylev3.New(endpoint)

	// Health check
	fmt.Fprintf(c.Out, "Checking %s ...\n", endpoint)
	if err := client.HealthCheck(context.Background()); err != nil {
		fmt.Fprintf(c.Out, "JEV v3 health check failed: %v\n", err)
		return c.retryV3Config()
	}

	// Test decision
	fmt.Fprintln(c.Out, "Testing decision...")
	testCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := client.DecideBool(testCtx,
		"The sky is clear and the sun is shining brightly.",
		"Is it currently raining?")
	if err != nil {
		fmt.Fprintf(c.Out, "JEV v3 test decision failed: %v\n", err)
		return c.retryV3Config()
	}

	fmt.Fprintf(c.Out, "JEV v3 test decision: %v (expected: false)\n", resp)
	if resp != false {
		fmt.Fprintln(c.Out, "Warning: unexpected test result. The server may not be fully compatible.")
		return c.retryV3Config()
	}

	// Success
	c.Cfg.JevstyleV3Endpoint = endpoint
	c.Cfg.JevstyleModel = ""
	fmt.Fprintf(c.Out, "JEV Style v3 configured: %s\n", endpoint)
	fmt.Fprintln(c.Out, "Run /save to persist.")
	if !c.Cfg.AssistedYes {
		fmt.Fprintln(c.Out, "Tip: Enable assisted execution mode with /yes assisted or /jevstyle assisted on.")
	}
	return nil
}

// retryV3Config asks the user to retry or cancel.
func (c *Ctx) retryV3Config() error {
	answer, err := c.Prompter.GetInput("Retry? [Y/n] (or 'cancel' to disable JEV Style): ")
	if err != nil {
		return nil
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer == "" || answer == "y" || answer == "yes" {
		return c.configureV3()
	}
	c.Cfg.JevstyleV3Endpoint = ""
	c.Cfg.JevstyleModel = ""
	c.Cfg.AssistedYes = false
	if c.Perm != nil {
		c.Perm.SetAssistedMode(false)
	}
	fmt.Fprintln(c.Out, "JEV Style disabled. Use /jevstyle to configure again.")
	return nil
}

func printJevstyleStatus(c *Ctx) {
	if c.Cfg.JevstyleV3InUse() {
		assisted := "off"
		if c.Cfg.AssistedYes {
			assisted = "on"
		}
		fmt.Fprintf(c.Out, "JEV Style: v3 (%s) [assisted mode: %s]\n",
			strings.TrimSpace(c.Cfg.JevstyleV3Endpoint), assisted)
		return
	}
	if strings.TrimSpace(c.Cfg.JevstyleModel) == "" {
		fmt.Fprintln(c.Out, "JEV Style: no model configured (JEVSTYLE_MODEL or JEVSTYLE_V3_ENDPOINT).")
	} else {
		assisted := "off"
		if c.Cfg.AssistedYes {
			assisted = "on"
		}
		fmt.Fprintf(c.Out, "JEV Style: v1/v2 (%s) [assisted mode: %s]\n",
			strings.TrimSpace(c.Cfg.JevstyleModel), assisted)
	}
}

func runJevstyleTest(c *Ctx) error {
	if c.Cfg.JevstyleV3InUse() {
		return c.runV3Test(c)
	}
	if strings.TrimSpace(c.Cfg.JevstyleModel) == "" {
		fmt.Fprintln(c.Out, "Cannot test JEV Style: no model configured. Use /jevstyle to configure.")
		return nil
	}
	if c.Client == nil {
		fmt.Fprintln(c.Out, "Cannot test JEV Style: no Ollama client available.")
		return nil
	}

	model := strings.TrimSpace(c.Cfg.JevstyleModel)
	fmt.Fprintf(c.Out, "Testing JEV Style v1/v2 decision model (%s)...\n", model)

	req := jevstyle.DecisionRequest{
		State:    "The sky is clear and the sun is shining brightly.",
		Question: "Is it currently raining?",
		Options:  []string{"Yes", "No"},
	}
	prompt, err := jevstyle.FormatPrompt(req.State, req.Question, req.Options)
	if err != nil {
		return err
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := c.Client.ChatSync(ctx, ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{Role: "user", Content: prompt},
		},
		Options: ollama.ChatOptions{
			Temperature: 0,
			NumPredict:  10,
		},
		Stream: false,
	})
	if err != nil {
		fmt.Fprintf(c.Out, "JEV Style test failed: %v\n", err)
		return nil
	}

	choice, idx, err := jevstyle.ParseChoice(resp.Content, len(req.Options))
	if err != nil {
		fmt.Fprintf(c.Out, "JEV Style test returned unparseable output: %v (raw: %q)\n", err, resp.Content)
		return nil
	}

	duration := time.Since(start).Round(time.Millisecond)
	fmt.Fprintf(c.Out, "JEV Style decision: %s. %s (in %v, raw: %q)\n", choice, req.Options[idx], duration, resp.Content)
	return nil
}

func (c *Ctx) runV3Test(c *Ctx) error {
	endpoint := strings.TrimSpace(c.Cfg.JevstyleV3Endpoint)
	fmt.Fprintf(c.Out, "Testing JEV Style v3 (%s)...\n", endpoint)

	client := jevstylev3.New(endpoint)

	// Health check
	if err := client.HealthCheck(context.Background()); err != nil {
		fmt.Fprintf(c.Out, "JEV v3 health check failed: %v\n", err)
		return nil
	}
	fmt.Fprintln(c.Out, "Health check: OK")

	// Test noul
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	isRaining, err := client.DecideBool(ctx,
		"The sky is clear and the sun is shining brightly.",
		"Is it currently raining?")
	if err != nil {
		fmt.Fprintf(c.Out, "JEV v3 noul test failed: %v\n", err)
		return nil
	}
	fmt.Fprintf(c.Out, "Noul test (is raining?): %v (expected: false)\n", isRaining)

	// Test choice
	intent, _, err := client.DecideChoice(ctx,
		"Hi, I was charged twice for my subscription this month. I want a refund.",
		"Which intent is this?",
		"billing_question", "cancel_subscription", "technical_issue")
	if err != nil {
		fmt.Fprintf(c.Out, "JEV v3 choice test failed: %v\n", err)
		return nil
	}
	fmt.Fprintf(c.Out, "Choice test (intent): %s\n", intent)

	// Test score
	scoreResp, err := client.Decide(ctx, jevstylev3.DecisionRequest{
		State:    "Ticket: the checkout page returns HTTP 500 for every customer since the last deploy 2 hours ago.",
		Question: "How urgent is this ticket?",
		Options:  []string{"not urgent", "normal", "urgent", "critical"},
	})
	if err != nil {
		fmt.Fprintf(c.Out, "JEV v3 score test failed: %v\n", err)
		return nil
	}
	fmt.Fprintf(c.Out, "Score test (urgency): %s (confidence: %.2f)\n",
		scoreResp.Option, scoreResp.Confidence)

	fmt.Fprintln(c.Out, "JEV v3 test complete.")
	return nil
}
