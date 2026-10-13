package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func newTodoTestAgent(t *testing.T) *Agent {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "m",
		ContextWindow: 8000,
		Cwd:           tmp,
		SessionsDir:   tmp,
	}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	reg.Register(tools.NewTodoWriteTool())
	perm := permissions.NewManager(&config.Config{YesMode: true})
	return New(cfg, fakeClient{}, reg, perm, sess, &fakeUI{})
}

func writeTodos(t *testing.T, ag *Agent, merge bool, items ...map[string]any) {
	t.Helper()
	tool := ag.reg.Get("TodoWrite")
	if tool == nil {
		t.Fatal("TodoWrite not registered")
	}
	raw := make([]any, 0, len(items))
	for _, it := range items {
		raw = append(raw, it)
	}
	res := tool.Execute(context.Background(), map[string]any{"merge": merge, "todos": raw})
	if res.IsError {
		t.Fatalf("todowrite failed: %s", res.Output)
	}
}

func TestTrailingStateNotePresentWithPendingTodos(t *testing.T) {
	t.Parallel()
	ag := newTodoTestAgent(t)
	writeTodos(t, ag, false,
		map[string]any{"id": "1", "content": "Write the code", "status": "in_progress"},
		map[string]any{"id": "2", "content": "Run the tests", "status": "pending"},
	)
	ag.sess.AddUser("do the work")

	msgs, _ := ag.buildOllamaMessages(context.Background(), "SYS")
	last := msgs[len(msgs)-1]
	if last.Role != "user" || !strings.HasPrefix(last.Content, session.RuntimeReminderPrefix) {
		t.Fatalf("expected trailing runtime reminder, got %+v", last)
	}
	if !strings.Contains(last.Content, "Checklist") || !strings.Contains(last.Content, "Run the tests") {
		t.Fatalf("reminder should carry the durable progress, got %q", last.Content)
	}
}

func TestTrailingStateNoteAbsentWithoutTodos(t *testing.T) {
	t.Parallel()
	ag := newTodoTestAgent(t)
	ag.sess.AddUser("do the work")

	msgs, _ := ag.buildOllamaMessages(context.Background(), "SYS")
	for _, m := range msgs {
		if strings.HasPrefix(m.Content, session.RuntimeReminderPrefix) {
			t.Fatalf("expected no runtime reminder without todos, got %q", m.Content)
		}
	}
}

func TestTrailingStateNoteNotPersisted(t *testing.T) {
	t.Parallel()
	ag := newTodoTestAgent(t)
	writeTodos(t, ag, false,
		map[string]any{"id": "1", "content": "Write the code", "status": "pending"},
	)
	ag.sess.AddUser("do the work")

	before := ag.sess.MessageCount()
	if _, _ = ag.buildOllamaMessages(context.Background(), "SYS"); ag.sess.MessageCount() != before {
		t.Fatal("runtime reminder must not be stored in the session transcript")
	}
}

func TestTrailingStateNoteAppearsOncePerRequest(t *testing.T) {
	t.Parallel()
	ag := newTodoTestAgent(t)
	writeTodos(t, ag, false,
		map[string]any{"id": "1", "content": "Write the code", "status": "pending"},
	)
	ag.sess.AddUser("do the work")

	for i := 0; i < 3; i++ {
		msgs, _ := ag.buildOllamaMessages(context.Background(), "SYS")
		n := 0
		for _, m := range msgs {
			if strings.HasPrefix(m.Content, session.RuntimeReminderPrefix) {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("expected exactly one runtime reminder per request, got %d", n)
		}
	}
}

func TestRuntimeStateNoteIncludesMissionTurns(t *testing.T) {
	t.Parallel()
	ag := newTodoTestAgent(t)
	ag.StartMission("long job")
	ag.mission.IncrementTurns()
	ag.mission.IncrementTurns()

	note := ag.runtimeStateNote()
	if !strings.Contains(note, "Mission continuation turns so far: 2") {
		t.Fatalf("expected mission turn count in note, got %q", note)
	}
}
