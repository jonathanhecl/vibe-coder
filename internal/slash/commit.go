package slash

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

func runCommitFlow(c *Ctx) (string, string, error) {
	if _, err := runGit(c.Cfg.Cwd, "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", "Not a git repository, skipping commit.", nil
	}
	diff, err := runGit(c.Cfg.Cwd, "diff", "--staged")
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(diff) == "" {
		unstaged, err := runGit(c.Cfg.Cwd, "diff")
		if err != nil {
			return "", "", err
		}
		if strings.TrimSpace(unstaged) == "" {
			return "", "No changes to commit.", nil
		}
		if _, err := runGit(c.Cfg.Cwd, "add", "-A"); err != nil {
			return "", "", err
		}
		diff, err = runGit(c.Cfg.Cwd, "diff", "--staged")
		if err != nil {
			return "", "", err
		}
	}

	msg := "chore: update project files"
	if c.Client != nil {
		promptDiff := diff
		if len(promptDiff) > 4096 {
			promptDiff = promptDiff[:4096]
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		resp, err := c.Client.ChatSync(ctx, ollama.ChatRequest{
			Model: c.Cfg.Model,
			Messages: []ollama.Message{
				{Role: "system", Content: "Return one concise conventional commit message only."},
				{Role: "user", Content: "Diff:\n" + promptDiff},
			},
			Stream: false,
		})
		if err == nil && strings.TrimSpace(resp.Content) != "" {
			msg = sanitizeCommitMessage(resp.Content)
		}
	}

	if _, err := runGit(c.Cfg.Cwd, "commit", "-m", msg); err != nil {
		return "", "", err
	}
	return msg, "", nil
}

func runGit(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func sanitizeCommitMessage(raw string) string {
	line := strings.TrimSpace(strings.Split(raw, "\n")[0])
	line = strings.Trim(line, "`\"")
	if line == "" {
		return "chore: update project files"
	}
	if len(line) > 72 {
		line = line[:72]
	}
	return line
}
