package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func TestReplanReminderOnChangeWithPendingTodos(t *testing.T) {
	t.Parallel()
	ag, _, reg := newTurnAgent(t, &fakeClient{})
	seedChecklist(t, reg, []tools.TodoItem{
		{ID: "1", Content: "done step", Status: tools.TodoStatusCompleted},
		{ID: "2", Content: "open step", Status: tools.TodoStatusPending},
	})
	ag.addRunContext(context.Background(), "quita el paso 2 y agrega validar")
	msgs := ag.sess.MessagesReadOnly()
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "First update the checklist with TodoWrite using full replace") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected replan reminder, got %+v", msgs)
	}
}

func TestNoReplanReminderOnContinue(t *testing.T) {
	t.Parallel()
	ag, _, reg := newTurnAgent(t, &fakeClient{})
	seedChecklist(t, reg, []tools.TodoItem{
		{ID: "1", Content: "open step", Status: tools.TodoStatusPending},
	})
	ag.addRunContext(context.Background(), "continua")
	msgs := ag.sess.MessagesReadOnly()
	for _, m := range msgs {
		if strings.Contains(m.Content, "First update the checklist") {
			t.Fatalf("continuation must not trigger replan: %q", m.Content)
		}
	}
}
