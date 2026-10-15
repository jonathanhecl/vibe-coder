package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/background"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func TestInjectCompletedBackgroundTasks(t *testing.T) {
	background.ResetDefaultManager()
	mgr := background.DefaultManager()

	task, err := mgr.Start("echo 'bg completion test'", "", 1*time.Second)
	if err != nil {
		t.Fatalf("start error: %v", err)
	}

	cfg := &config.Config{Cwd: "."}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	reg.RegisterDefaults()

	ag := New(cfg, nil, reg, nil, sess, nil)

	// Call injectCompletedBackgroundTasks
	ag.injectCompletedBackgroundTasks()

	// Check messages in session
	msgs := sess.Messages()
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Content, task.ID) && strings.Contains(m.Content, "completed") {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("expected completed task %s runtime reminder in session messages", task.ID)
	}

	// Calling it again should not duplicate because it was drained
	ag.injectCompletedBackgroundTasks()
	msgsAfter := sess.Messages()
	if len(msgsAfter) != len(msgs) {
		t.Fatalf("expected no new messages after second drain, got %d vs %d", len(msgsAfter), len(msgs))
	}
}
