package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func queueModel(prompts ...string) hybridInputModel {
	return newHybridInputModel("> ", StatusInfo{}, nil, nil, 100).withQueue(prompts)
}

func pressKey(t *testing.T, m hybridInputModel, key tea.KeyType) hybridInputModel {
	t.Helper()
	next, _ := m.Update(tea.KeyMsg{Type: key})
	model, ok := next.(hybridInputModel)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return model
}

// Enter on an empty line runs the selected queued prompt; the rest of the queue
// stays for the next prompt instead of being dropped.
func TestQueuedPromptRunsOnEmptyEnter(t *testing.T) {
	m := queueModel("primero", "segundo")
	m = pressKey(t, m, tea.KeyCtrlN)
	if m.queueIdx != 1 {
		t.Fatalf("selected=%d, want 1", m.queueIdx)
	}

	m = pressKey(t, m, tea.KeyEnter)
	if !m.submitted {
		t.Fatal("Enter must submit the selected queued prompt")
	}
	if got := m.value(); got != "segundo" {
		t.Fatalf("value=%q, want the second queued prompt", got)
	}
	if len(m.queue) != 1 || m.queue[0] != "primero" {
		t.Fatalf("remaining queue=%#v", m.queue)
	}
}

// A non-empty input line keeps priority: the queue is untouched.
func TestQueuedPromptDoesNotHijackTypedInput(t *testing.T) {
	m := queueModel("viejo")
	m = pressKey(t, m, tea.KeyRunes)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("nuevo")})
	m = next.(hybridInputModel)

	m = pressKey(t, m, tea.KeyEnter)
	if got := m.value(); got != "nuevo" {
		t.Fatalf("value=%q, want the typed line", got)
	}
	if len(m.queue) != 1 || m.queue[0] != "viejo" {
		t.Fatalf("queue must survive a typed send: %#v", m.queue)
	}
}

// Ctrl+X discards the selection and Ctrl+O loads it for editing.
func TestQueuedPromptDiscardAndEdit(t *testing.T) {
	m := queueModel("a", "b")
	m = pressKey(t, m, tea.KeyCtrlX)
	if len(m.queue) != 1 || m.queue[0] != "b" {
		t.Fatalf("after discard queue=%#v", m.queue)
	}

	m = pressKey(t, m, tea.KeyCtrlO)
	if m.value() != "b" {
		t.Fatalf("Ctrl+O must load the prompt for editing, got %q", m.value())
	}
	if len(m.queue) != 0 {
		t.Fatalf("an edited prompt must leave the queue so it cannot send twice: %#v", m.queue)
	}
}

// The queue is visible above the footer, with the selected entry marked.
func TestQueuedPromptsRender(t *testing.T) {
	m := queueModel("arreglá el test", "después documentá")
	m = pressKey(t, m, tea.KeyCtrlN)

	view := m.View()
	for _, want := range []string{"queued", "2/2", "arreglá el test", "después documentá", "›"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}
