package agent

import (
	"context"
	"encoding/json"
	"os"
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
