package slash

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/jevstyle"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

func runJevstyleCommand(c *Ctx, args []string) error {
	if len(args) == 0 {
		printJevstyleStatus(c)
		return nil
	}
	sub := strings.TrimSpace(args[0])
	switch strings.ToLower(sub) {
	case "status":
		printJevstyleStatus(c)
	case "test":
		return runJevstyleTest(c)
	case "off", "disable", "none":
		c.Cfg.JevstyleModel = ""
		fmt.Fprintln(c.Out, "JEV Style model disabled for this session.")
	default:
		if strings.EqualFold(sub, "set") && len(args) > 1 {
			sub = strings.TrimSpace(args[1])
		} else if len(args) > 1 {
			fmt.Fprintln(c.Out, "Invalid model name format.")
			return nil
		}
		if !modelNameRe.MatchString(sub) {
			fmt.Fprintln(c.Out, "Invalid model name format.")
			return nil
		}
		c.Cfg.JevstyleModel = sub
		fmt.Fprintf(c.Out, "JEV Style model set to: %s (run /save to persist)\n", c.Cfg.JevstyleModel)
	}
	return nil
}

func printJevstyleStatus(c *Ctx) {
	if strings.TrimSpace(c.Cfg.JevstyleModel) == "" {
		fmt.Fprintln(c.Out, "JEV Style: no model configured (JEVSTYLE_MODEL).")
	} else {
		fmt.Fprintf(c.Out, "JEV Style: on (%s)\n", strings.TrimSpace(c.Cfg.JevstyleModel))
	}
}

func runJevstyleTest(c *Ctx) error {
	if strings.TrimSpace(c.Cfg.JevstyleModel) == "" {
		fmt.Fprintln(c.Out, "Cannot test JEV Style: no model configured. Use /jevstyle <model> or set JEVSTYLE_MODEL.")
		return nil
	}
	if c.Client == nil {
		fmt.Fprintln(c.Out, "Cannot test JEV Style: no Ollama client available.")
		return nil
	}

	model := strings.TrimSpace(c.Cfg.JevstyleModel)
	fmt.Fprintf(c.Out, "Testing JEV Style decision model (%s)...\n", model)

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
