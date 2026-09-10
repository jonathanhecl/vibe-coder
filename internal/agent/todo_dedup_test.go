package agent

import (
	"context"
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

func TestTodoProgressNoteInjectedOnceWhileUnchanged(t *testing.T) {
	t.Parallel()
	ag := newTodoTestAgent(t)
	writeTodos(t, ag, false,
		map[string]any{"id": "1", "content": "Write the code", "status": "in_progress"},
		map[string]any{"id": "2", "content": "Run the tests", "status": "pending"},
	)

	before := ag.sess.MessageCount()
	for i := 0; i < 5; i++ {
		ag.addTodoProgressNoteIfChanged()
	}
	if got := ag.sess.MessageCount() - before; got != 1 {
		t.Fatalf("expected exactly 1 injected note across 5 iterations, got %d", got)
	}
}

func TestTodoProgressNoteReInjectedOnChange(t *testing.T) {
	t.Parallel()
	ag := newTodoTestAgent(t)
	writeTodos(t, ag, false,
		map[string]any{"id": "1", "content": "Write the code", "status": "in_progress"},
	)
	ag.addTodoProgressNoteIfChanged()
	before := ag.sess.MessageCount()

	// Status flip changes the note: it must be injected again.
	writeTodos(t, ag, true,
		map[string]any{"id": "1", "content": "Write the code", "status": "completed"},
	)
	ag.addTodoProgressNoteIfChanged()
	if got := ag.sess.MessageCount() - before; got != 1 {
		t.Fatalf("expected re-injection after change, got %d new messages", got)
	}

	// Unchanged again: no new message.
	ag.addTodoProgressNoteIfChanged()
	if got := ag.sess.MessageCount() - before; got != 1 {
		t.Fatalf("expected no further injection, got %d new messages", got)
	}
}

func TestTodoNoteDedupResetsPerRun(t *testing.T) {
	t.Parallel()
	ag := newTodoTestAgent(t)
	writeTodos(t, ag, false,
		map[string]any{"id": "1", "content": "Write the code", "status": "pending"},
	)
	ag.addTodoProgressNoteIfChanged()
	before := ag.sess.MessageCount()
	ag.resetTodoNoteDedup()
	ag.addTodoProgressNoteIfChanged()
	if got := ag.sess.MessageCount() - before; got != 1 {
		t.Fatalf("expected re-injection after reset, got %d", got)
	}
}

func TestNoNoteWithoutTodos(t *testing.T) {
	t.Parallel()
	ag := newTodoTestAgent(t)
	before := ag.sess.MessageCount()
	ag.addTodoProgressNoteIfChanged()
	if ag.sess.MessageCount() != before {
		t.Fatal("expected no note without todos")
	}
}
