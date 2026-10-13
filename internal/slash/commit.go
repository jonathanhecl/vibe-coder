package slash

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/decision"
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
			Stream:    false,
			KeepAlive: c.Cfg.KeepAliveFor(config.RoleMain),
		})
		if err == nil && strings.TrimSpace(resp.Content) != "" {
			msg = sanitizeCommitMessage(resp.Content)
		}

		if c.Cfg.DecisionInUse() {
			decisionCtx, decisionCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer decisionCancel()
			dec := decision.FromConfig(c.Cfg)
			if cType, err := dec.ClassifyCommit(decisionCtx, promptDiff); err == nil && cType != "" {
				msg = ensureCommitTypePrefix(msg, cType)
			}
		}
	}

	if _, err := runGit(c.Cfg.Cwd, "commit", "-m", msg); err != nil {
		return "", "", err
	}
	return msg, "", nil
}

func ensureCommitTypePrefix(msg, commitType string) string {
	msg = strings.TrimSpace(msg)
	commitType = strings.ToLower(strings.TrimSpace(commitType))
	if commitType == "" {
		return msg
	}
	parts := strings.SplitN(msg, ":", 2)
	if len(parts) == 2 {
		prefix := strings.ToLower(strings.TrimSpace(parts[0]))
		switch prefix {
		case "feat", "fix", "docs", "style", "refactor", "perf", "test", "build", "ci", "chore", "revert":
			return msg
		}
	}
	return fmt.Sprintf("%s: %s", commitType, msg)
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
