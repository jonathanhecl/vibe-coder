package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// decisionUI answers every permission prompt the same way and records the
// tools it was asked about, so a test can tell a denial from a cancellation and
// count how many prompts the run produced.
type decisionUI struct {
	fakeUI
	decision tui.Decision
	asked    []string
}

func (u *decisionUI) AskPermission(tool string, _ map[string]any) tui.Decision {
	u.asked = append(u.asked, tool)
	return u.decision
}

func newPermissionAgent(t *testing.T, client ollama.Client, decision tui.Decision) (*Agent, *decisionUI) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 4096,
		MaxTokens:     128,
		Temperature:   0.2,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: false})
	sess := session.New(cfg)
	ui := &decisionUI{decision: decision}
	return New(cfg, client, reg, perm, sess, ui), ui
}

func sessionText(ag *Agent) string {
	var b strings.Builder
	for _, msg := range ag.sess.Messages() {
		b.WriteString(msg.Role)
		b.WriteString(": ")
		b.WriteString(msg.Content)
		b.WriteString("\n")
	}
	return b.String()
}

// A denial refuses one action, not the run: the model reads the refusal, keeps
// its turn open, and gets another chance to finish the task another way.
func TestDeniedToolKeepsTheRunGoing(t *testing.T) {
	target := "denied.txt"
	client := &sequenceClient{replies: []string{
		`<invoke name="Write">{"file_path":"` + target + `","contents":"unexpected"}</invoke>`,
		"done without the file",
	}}
	ag, ui := newPermissionAgent(t, client, tui.DecisionDenyOnce)

	if err := ag.Run(context.Background(), "write the file"); err != nil {
		t.Fatalf("a denial must not end the run with an error: %v", err)
	}
	if client.calls != 2 {
		t.Fatalf("expected the run to continue after the denial: model turns=%d, want 2", client.calls)
	}
	if len(ui.asked) != 1 {
		t.Fatalf("expected exactly one prompt, got %v", ui.asked)
	}
	text := sessionText(ag)
	if !strings.Contains(text, "Permission denied") {
		t.Fatalf("the model was not told about the denial:\n%s", text)
	}
	if !strings.Contains(text, permissionDenialGuidance) {
		t.Fatalf("the denial did not carry the adapt-and-continue guidance:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(ag.cfg.Cwd, target)); !os.IsNotExist(err) {
		t.Fatalf("denied write still landed: %v", err)
	}
}

// Cancel is the only choice that ends the run: the next model turn is never
// requested and control returns to the user prompt.
func TestCancelEndsTheRunWithoutAnotherModelTurn(t *testing.T) {
	client := &sequenceClient{replies: []string{
		`<invoke name="Write">{"file_path":"cancelled.txt","contents":"x"}</invoke>`,
		"this reply must never be requested",
	}}
	ag, _ := newPermissionAgent(t, client, tui.DecisionCancel)

	if err := ag.Run(context.Background(), "write the file"); err != nil {
		t.Fatalf("cancel should end the run quietly: %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("cancel must stop the run: model turns=%d, want 1", client.calls)
	}
	if text := sessionText(ag); !strings.Contains(text, "Cancelled") {
		t.Fatalf("cancel was not recorded for the model:\n%s", text)
	}
}

// In a native batch a denied call no longer poisons the other calls: the ones
// the user never refused still run, and the batch hands its results back.
func TestNativeBatchDenialKeepsOtherCalls(t *testing.T) {
	readable := "note.txt"
	client := &nativeSequenceClient{turns: []ollama.Chunk{
		{ToolCalls: []ollama.MessageToolCall{
			{Function: ollama.MessageToolCallFunction{Name: "Write", Arguments: map[string]any{"file_path": "blocked.txt", "contents": "x"}}},
			{Function: ollama.MessageToolCallFunction{Name: "Read", Arguments: map[string]any{"file_path": readable}}},
		}, Done: true},
		{Delta: "done", Done: true},
	}}
	ag, ui := newPermissionAgent(t, client, tui.DecisionDenyOnce)
	// The Read target must exist for the call to be meaningful.
	if err := os.WriteFile(filepath.Join(ag.cfg.Cwd, readable), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ag.Run(context.Background(), "read the note and write a file"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(ui.asked) != 1 || ui.asked[0] != "Write" {
		t.Fatalf("expected one prompt for Write, got %v", ui.asked)
	}
	text := sessionText(ag)
	if !strings.Contains(text, "blocked.txt") || !strings.Contains(text, "Permission denied") {
		t.Fatalf("the denial result was not recorded in the batch:\n%s", text)
	}
	if !strings.Contains(text, "hello") {
		t.Fatalf("the call after the denial never ran:\n%s", text)
	}
}

// Cancelling inside a native batch stops the batch and the run: the calls left
// in it are recorded as skipped, not executed.
func TestNativeBatchCancelSkipsRemainingCalls(t *testing.T) {
	client := &nativeSequenceClient{turns: []ollama.Chunk{
		{ToolCalls: []ollama.MessageToolCall{
			{Function: ollama.MessageToolCallFunction{Name: "Write", Arguments: map[string]any{"file_path": "first.txt", "contents": "x"}}},
			{Function: ollama.MessageToolCallFunction{Name: "Write", Arguments: map[string]any{"file_path": "second.txt", "contents": "y"}}},
		}, Done: true},
		{Delta: "this reply must never be requested", Done: true},
	}}
	ag, ui := newPermissionAgent(t, client, tui.DecisionCancel)

	if err := ag.Run(context.Background(), "write both files"); err != nil {
		t.Fatalf("cancel should end the run quietly: %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("cancel must stop the run: model turns=%d, want 1", client.calls)
	}
	if len(ui.asked) != 1 {
		t.Fatalf("cancel must stop asking: prompts=%v", ui.asked)
	}
	text := sessionText(ag)
	if !strings.Contains(text, "the user cancelled this run") {
		t.Fatalf("remaining calls were not recorded as cancelled:\n%s", text)
	}
}
