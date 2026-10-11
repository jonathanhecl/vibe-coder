package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func TestProgressPromptBlock(t *testing.T) {
	ag := newMissionAgent(t)
	if block := ag.progressPromptBlock(); block != "" {
		t.Fatalf("expected empty progress block, got %q", block)
	}
	ag.todoWriteTool().Store().Replace([]tools.TodoItem{
		{ID: "1", Content: "done step", Status: tools.TodoStatusCompleted},
		{ID: "2", Content: "next step", Status: tools.TodoStatusPending},
	})
	block := ag.progressPromptBlock()
	if !strings.Contains(block, "1 done") {
		t.Fatalf("expected done count, got %q", block)
	}
	if !strings.Contains(block, "next step") {
		t.Fatalf("expected next pending content, got %q", block)
	}
	if !strings.Contains(block, "durable") {
		t.Fatalf("expected durability note, got %q", block)
	}
}

func TestProgressCacheKeyChangesWithChecklist(t *testing.T) {
	ag := newMissionAgent(t)
	before := ag.progressCacheKey()
	ag.todoWriteTool().Store().Replace([]tools.TodoItem{
		{ID: "1", Content: "step", Status: tools.TodoStatusPending},
	})
	if after := ag.progressCacheKey(); after == before {
		t.Fatal("expected progress cache key to change when the checklist changes")
	}
}

func TestRunLogWritesRedactedJSONL(t *testing.T) {
	ag := newMissionAgent(t)
	ag.reg.Get("MissionStart").Execute(context.Background(), map[string]any{
		"goal": "process GITHUB_TOKEN=ghp_secret items",
	})
	ag.resetTurnToolCalls()
	ag.noteToolExecuted()
	ag.LogMissionTurn(nil)

	path := ag.RunLogPath()
	if path == "" {
		t.Fatal("expected a run log path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read run log: %v", err)
	}
	if strings.Contains(string(data), "ghp_secret") {
		t.Fatalf("run log leaked a secret: %s", data)
	}
	var entry runLogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &entry); err != nil {
		t.Fatalf("decode run log: %v", err)
	}
	if entry.Status != tools.MissionStatusActive || entry.Tools != 1 {
		t.Fatalf("unexpected run log entry: %#v", entry)
	}
}

func TestRunLogIsolated_ScopesToWorkspaceAndNeverWritesToStateDir(t *testing.T) {
	ag := newMissionAgent(t)
	ag.cfg.Isolated = true

	path := ag.RunLogPath()
	wantPath := filepath.Join(ag.cfg.Cwd, ".vibe-isolated-runs.jsonl")
	if path != wantPath {
		t.Fatalf("expected RunLogPath %q, got %q", wantPath, path)
	}

	ag.reg.Get("MissionStart").Execute(context.Background(), map[string]any{
		"goal": "autonomous task in isolated session",
	})
	ag.resetTurnToolCalls()
	ag.noteToolExecuted()
	ag.LogMissionTurn(nil)

	// Workspace run log must exist
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected isolated run log at %q: %v", wantPath, err)
	}

	// StateDir/runs must NOT exist or be created
	stateRunsDir := filepath.Join(ag.cfg.StateDir, "runs")
	if _, err := os.Stat(stateRunsDir); !os.IsNotExist(err) {
		t.Fatalf("expected state runs dir %q to NOT exist, but it was created", stateRunsDir)
	}
}

func TestRunLogTemporal_SuppressesRunLog(t *testing.T) {
	ag := newMissionAgent(t)
	ag.cfg.Temporal = true

	path := ag.RunLogPath()
	if path != "" {
		t.Fatalf("expected empty RunLogPath for temporal session, got %q", path)
	}

	ag.reg.Get("MissionStart").Execute(context.Background(), map[string]any{
		"goal": "autonomous task in temporal session",
	})
	ag.resetTurnToolCalls()
	ag.noteToolExecuted()
	ag.LogMissionTurn(nil)

	// No file should have been written to StateDir or Cwd
	stateRunsDir := filepath.Join(ag.cfg.StateDir, "runs")
	if _, err := os.Stat(stateRunsDir); !os.IsNotExist(err) {
		t.Fatalf("expected state runs dir %q to NOT exist for temporal session", stateRunsDir)
	}
}
