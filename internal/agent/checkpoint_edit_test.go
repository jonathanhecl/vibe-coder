package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/git"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// A checkpoint is a safety net, not a precondition for editing. Targets the
// snapshot cannot cover, such as files outside the repository, must leave the
// edit running instead of aborting the whole turn.
func TestEditOutsideRepositorySkipsCheckpointWithoutAborting(t *testing.T) {
	ag := newCoverageAgent(t, fakeClient{}, tui.DecisionAllowOnce, false)
	cmd := exec.Command("git", "init")
	cmd.Dir = ag.cfg.Cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	tool := ag.reg.Get("Write")
	result, outcome, err := ag.executeTool(context.Background(), tool, tool.Name(), map[string]any{
		"file_path": outside,
		"contents":  "written",
	}, toolExecutionMode{})
	if err != nil {
		t.Fatalf("checkpoint aborted the edit: %v", err)
	}
	if !outcome.ran() || result.IsError {
		t.Fatalf("write did not run: %+v, outcome=%v", result, outcome)
	}
	data, readErr := os.ReadFile(outside)
	if readErr != nil || string(data) != "written" {
		t.Fatalf("file not written: %q, %v", data, readErr)
	}
	if err := git.NewCheckpoint(ag.cfg.Cwd).Rollback(); !errors.Is(err, git.ErrNoCheckpoint) {
		t.Fatalf("outside edit was checkpointed: %v", err)
	}
}
