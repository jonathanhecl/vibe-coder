package git

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Checkpoint struct {
	cwd string
}

func NewCheckpoint(cwd string) *Checkpoint {
	return &Checkpoint{cwd: cwd}
}

func (c *Checkpoint) IsRepo() bool {
	_, err := c.run("rev-parse", "--is-inside-work-tree")
	return err == nil
}

func (c *Checkpoint) Create(label string) error {
	if !c.IsRepo() {
		return nil
	}
	stashLabel := fmt.Sprintf("vibe-coder/%s/%d", label, time.Now().Unix())
	out, err := c.run("stash", "push", "--include-untracked", "--keep-index", "-m", stashLabel)
	if err != nil {
		low := strings.ToLower(out)
		if strings.Contains(low, "no local changes") || strings.Contains(low, "no changes") {
			return nil
		}
		return err
	}
	return nil
}

func (c *Checkpoint) Rollback() error {
	if !c.IsRepo() {
		return nil
	}
	list, err := c.run("stash", "list")
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(list), "\n")
	targetRef := ""
	for _, line := range lines {
		if strings.Contains(line, "vibe-coder/") {
			ref := strings.SplitN(line, ":", 2)[0]
			targetRef = strings.TrimSpace(ref)
			break
		}
	}
	if targetRef == "" {
		return nil
	}
	_, err = c.run("stash", "pop", targetRef)
	return err
}

func (c *Checkpoint) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = c.cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
