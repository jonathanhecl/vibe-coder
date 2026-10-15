package background

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBackgroundStartFast(t *testing.T) {
	mgr := NewManager()
	task, err := mgr.Start("echo 'hello fast'", "", 2*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.ID == "" {
		t.Fatalf("expected task ID, got empty")
	}
	if task.Status != StatusCompleted {
		t.Fatalf("expected completed status, got %s", task.Status)
	}
	if task.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", task.ExitCode)
	}
	if !strings.Contains(task.Output(), "hello fast") {
		t.Fatalf("expected output to contain 'hello fast', got %q", task.Output())
	}
}

func TestBackgroundStartLongAndPoll(t *testing.T) {
	mgr := NewManager()
	task, err := mgr.Start("sleep 1 && echo 'slept 1s'", "", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !task.IsRunning() {
		t.Fatalf("expected task to be running after 100ms, got status %s", task.Status)
	}
	if task.Status != StatusRunning {
		t.Fatalf("expected status running, got %s", task.Status)
	}

	// Wait with timeout
	ctx := context.Background()
	waitedTask, finished, err := mgr.Wait(ctx, task.ID, 2*time.Second)
	if err != nil {
		t.Fatalf("wait error: %v", err)
	}
	if !finished {
		t.Fatalf("expected task to finish within 2s")
	}
	if waitedTask.Status != StatusCompleted {
		t.Fatalf("expected status completed, got %s", waitedTask.Status)
	}
	if !strings.Contains(waitedTask.Output(), "slept 1s") {
		t.Fatalf("expected output to contain 'slept 1s', got %q", waitedTask.Output())
	}
}

func TestBackgroundKill(t *testing.T) {
	mgr := NewManager()
	task, err := mgr.Start("sleep 30", "", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !task.IsRunning() {
		t.Fatalf("expected task to be running")
	}

	if err := mgr.Kill(task.ID); err != nil {
		t.Fatalf("failed to kill task: %v", err)
	}

	if task.IsRunning() {
		t.Fatalf("expected task to not be running after kill")
	}
	if task.Status != StatusKilled {
		t.Fatalf("expected status killed, got %s", task.Status)
	}
}

func TestBackgroundDrainUnnotified(t *testing.T) {
	mgr := NewManager()
	task, err := mgr.Start("echo 'task 1'", "", 1*time.Second)
	if err != nil {
		t.Fatalf("start error: %v", err)
	}

	drained := mgr.DrainUnnotifiedCompleted()
	if len(drained) != 1 || drained[0].ID != task.ID {
		t.Fatalf("expected 1 drained task, got %d", len(drained))
	}

	// Second drain should return empty
	drained2 := mgr.DrainUnnotifiedCompleted()
	if len(drained2) != 0 {
		t.Fatalf("expected 0 drained tasks on second call, got %d", len(drained2))
	}
}

func TestDangerousCommandBlocked(t *testing.T) {
	mgr := NewManager()
	_, err := mgr.Start("rm -rf /", "", 100*time.Millisecond)
	if err == nil {
		t.Fatalf("expected dangerous command to be blocked, got nil error")
	}
	if !strings.Contains(err.Error(), "dangerous command blocked") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
