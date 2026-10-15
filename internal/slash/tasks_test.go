package slash

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/background"
)

func TestSlashTasksCommand(t *testing.T) {
	background.ResetDefaultManager()
	mgr := background.DefaultManager()

	task, err := mgr.Start("sleep 5", "", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected start error: %v", err)
	}

	var buf bytes.Buffer
	c := &Ctx{Out: &buf}

	// 1. Test /tasks list
	if err := runTasksCommand(c, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, task.ID) || !strings.Contains(out, "RUNNING") {
		t.Fatalf("expected task %s in output: %s", task.ID, out)
	}

	// 2. Test /tasks kill
	buf.Reset()
	if err := runTasksCommand(c, []string{"kill", task.ID}); err != nil {
		t.Fatalf("unexpected kill error: %v", err)
	}
	if !strings.Contains(buf.String(), "terminated") {
		t.Fatalf("expected termination confirmation, got: %s", buf.String())
	}
}
