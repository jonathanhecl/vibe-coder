package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/background"
)

func TestBackgroundToolsLifecycle(t *testing.T) {
	background.ResetDefaultManager()

	runTool := NewBackgroundRunTool()
	statusTool := NewBackgroundStatusTool()
	listTool := NewBackgroundListTool()
	killTool := NewBackgroundKillTool()

	ctx := context.Background()

	// 1. Run command that stays alive
	runRes := runTool.Execute(ctx, map[string]any{
		"command":         "sleep 5 && echo done",
		"initial_wait_ms": 100,
	})
	if runRes.IsError {
		t.Fatalf("runTool error: %s", runRes.Output)
	}
	if !strings.Contains(runRes.Output, "STILL RUNNING") {
		t.Fatalf("expected STILL RUNNING in output: %s", runRes.Output)
	}
	if !strings.Contains(runRes.Output, "[task_id: bg-1]") {
		t.Fatalf("expected task_id bg-1 in output: %s", runRes.Output)
	}

	// 2. List tools
	listRes := listTool.Execute(ctx, map[string]any{})
	if !strings.Contains(listRes.Output, "bg-1") || !strings.Contains(listRes.Output, "RUNNING") {
		t.Fatalf("expected bg-1 running in list output: %s", listRes.Output)
	}

	// 3. Status tool with instant check (wait_ms = 0)
	statusRes := statusTool.Execute(ctx, map[string]any{
		"task_id": "bg-1",
		"wait_ms": 0,
	})
	if !strings.Contains(statusRes.Output, "STILL RUNNING") {
		t.Fatalf("expected STILL RUNNING in status output: %s", statusRes.Output)
	}

	// 4. Kill tool
	killRes := killTool.Execute(ctx, map[string]any{
		"task_id": "bg-1",
	})
	if killRes.IsError {
		t.Fatalf("killTool error: %s", killRes.Output)
	}
	if !strings.Contains(killRes.Output, "terminated successfully") {
		t.Fatalf("expected termination message, got: %s", killRes.Output)
	}

	// 5. Status after kill
	statusRes2 := statusTool.Execute(ctx, map[string]any{
		"task_id": "bg-1",
	})
	if !strings.Contains(statusRes2.Output, "KILLED") {
		t.Fatalf("expected KILLED in status output: %s", statusRes2.Output)
	}
}

func TestBackgroundRunFastCompletion(t *testing.T) {
	background.ResetDefaultManager()

	runTool := NewBackgroundRunTool()
	ctx := context.Background()

	res := runTool.Execute(ctx, map[string]any{
		"command":         "echo 'hello background'",
		"initial_wait_ms": 2000,
	})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "COMPLETED") {
		t.Fatalf("expected COMPLETED in output: %s", res.Output)
	}
	if !strings.Contains(res.Output, "hello background") {
		t.Fatalf("expected output content, got: %s", res.Output)
	}
}

func TestBackgroundStatusWaitCompletion(t *testing.T) {
	background.ResetDefaultManager()

	runTool := NewBackgroundRunTool()
	statusTool := NewBackgroundStatusTool()
	ctx := context.Background()

	runRes := runTool.Execute(ctx, map[string]any{
		"command":         "sleep 0.5 && echo 'delayed output'",
		"initial_wait_ms": 50,
	})
	if !strings.Contains(runRes.Output, "STILL RUNNING") {
		t.Fatalf("expected STILL RUNNING: %s", runRes.Output)
	}

	// Wait up to 2 seconds for completion
	statusRes := statusTool.Execute(ctx, map[string]any{
		"task_id": "bg-1",
		"wait_ms": 2000,
	})
	if statusRes.IsError {
		t.Fatalf("expected success, got error: %s", statusRes.Output)
	}
	if !strings.Contains(statusRes.Output, "COMPLETED") {
		t.Fatalf("expected COMPLETED in status: %s", statusRes.Output)
	}
	if !strings.Contains(statusRes.Output, "delayed output") {
		t.Fatalf("expected delayed output in status: %s", statusRes.Output)
	}
}

func TestBackgroundStatusStripsANSI(t *testing.T) {
	background.ResetDefaultManager()

	runTool := NewBackgroundRunTool()
	statusTool := NewBackgroundStatusTool()
	ctx := context.Background()

	runRes := runTool.Execute(ctx, map[string]any{
		"command":         `printf "\033[32mcolorized success\033[0m"`,
		"initial_wait_ms": 2000,
	})
	if strings.Contains(runRes.Output, "\033[32m") {
		t.Fatalf("expected ANSI codes to be stripped in runRes, got: %q", runRes.Output)
	}
	if !strings.Contains(runRes.Output, "colorized success") {
		t.Fatalf("expected text content present in runRes, got: %q", runRes.Output)
	}

	statusRes := statusTool.Execute(ctx, map[string]any{
		"task_id": "bg-1",
	})
	if strings.Contains(statusRes.Output, "\033[32m") {
		t.Fatalf("expected ANSI codes to be stripped in statusRes, got: %q", statusRes.Output)
	}
	if !strings.Contains(statusRes.Output, "colorized success") {
		t.Fatalf("expected text content present in statusRes, got: %q", statusRes.Output)
	}
}
