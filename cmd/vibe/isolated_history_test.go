package main

import (
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// seedRecordingUI implements tui.UI by embedding the interface and records
// the prompts SeedHistory receives.
type seedRecordingUI struct {
	tui.UI
	prompts []string
}

func (u *seedRecordingUI) SeedHistory(entries []string) {
	u.prompts = entries
}

// plainUIStub satisfies tui.UI without SeedHistory, like a test fake.
type plainUIStub struct {
	tui.UI
}

// TestSeedIsolatedPromptHistory verifies an isolated resume seeds the input
// history with the session's user prompts.
func TestSeedIsolatedPromptHistory(t *testing.T) {
	sess := session.New(&config.Config{Cwd: t.TempDir()})
	sess.AddUser("first ask")
	sess.AddAssistant("answer")
	sess.AddUser("second ask")

	ui := &seedRecordingUI{}
	seedIsolatedPromptHistory(ui, sess)
	want := []string{"first ask", "second ask"}
	if len(ui.prompts) != len(want) {
		t.Fatalf("seeded prompts = %v, want %v", ui.prompts, want)
	}
	for i := range want {
		if ui.prompts[i] != want[i] {
			t.Fatalf("seeded prompts[%d] = %q, want %q", i, ui.prompts[i], want[i])
		}
	}
}

// TestSeedIsolatedPromptHistorySkipsUIsWithoutSeed verifies the type
// assertion keeps UIs and fakes without SeedHistory untouched.
func TestSeedIsolatedPromptHistorySkipsUIsWithoutSeed(t *testing.T) {
	sess := session.New(&config.Config{Cwd: t.TempDir()})
	sess.AddUser("ask")

	// Must not panic: the stub has no SeedHistory method.
	seedIsolatedPromptHistory(&plainUIStub{}, sess)
	seedIsolatedPromptHistory(nil, sess)
}
