package git

import (
	"fmt"
	"os/exec"
	"path/filepath"
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

func (c *Checkpoint) Create(label string, paths ...string) error {
	if !c.IsRepo() {
		return nil
	}
	stashLabel := fmt.Sprintf("vibe-coder/%s/%d", label, time.Now().Unix())
	rel := relPathsInsideRepo(c.cwd, paths)
	if len(rel) > 0 {
		// Prefer a pathspec stash: only the edited file is snapshotted, so
		// big repos skip the full-workdir scan and unrelated dirty files
		// stay exactly where they were.
		args := append([]string{"stash", "push", "--include-untracked", "--keep-index", "-m", stashLabel, "--"}, rel...)
		if out, err := c.run(args...); err == nil {
			return nil
		} else if low := strings.ToLower(out); !strings.Contains(low, "pathspec") && !strings.Contains(low, "did not match") {
			return err
		}
		// Pathspec rejected (e.g. ignored file): fall back to the full stash
		// so every other dirty file is still protected as before.
	}
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

// relPathsInsideRepo converts absolute paths to cwd-relative pathspec
// entries, dropping anything outside the repo tree.
func relPathsInsideRepo(cwd string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		rel := trimmed
		if filepath.IsAbs(trimmed) {
			r, err := filepath.Rel(cwd, trimmed)
			if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
				continue
			}
			rel = r
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
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
