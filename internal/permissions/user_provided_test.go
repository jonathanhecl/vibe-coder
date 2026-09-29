package permissions

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/jevstylev3"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// contextSafetyDecider implements the full optional chain: SafetyDecider,
// safetyDecisionMaker, and safetyContextDecisionMaker. It records the context
// note so tests can assert what the decision model was told.
type contextSafetyDecider struct {
	enabled bool
	note    string
	detail  string
	called  bool
}

func (d *contextSafetyDecider) Enabled() bool { return d.enabled }

func (d *contextSafetyDecider) IsCommandDangerous(context.Context, string) (bool, error) {
	return false, nil
}

func (d *contextSafetyDecider) AskSafety(_ context.Context, _ string, detail string) (*jevstylev3.SafetyDecision, error) {
	d.called = true
	d.detail = detail
	return &jevstylev3.SafetyDecision{Action: "allow", Confidence: 0.95}, nil
}

func (d *contextSafetyDecider) AskSafetyWithContext(_ context.Context, _, detail, note string) (*jevstylev3.SafetyDecision, error) {
	d.called = true
	d.detail = detail
	d.note = note
	return &jevstylev3.SafetyDecision{Action: "allow", Confidence: 0.95}, nil
}

func TestNoteUserProvidedPath_MatchesAbsPathAndBasename(t *testing.T) {
	m := NewManager(&config.Config{})
	dir := t.TempDir()
	img := filepath.Join(dir, "vibe_clipboard_123.png")
	m.NoteUserProvidedPath(img)

	if note := m.userProvidedNote("ls -l " + img); !strings.Contains(note, img) {
		t.Errorf("absolute path not reported, got %q", note)
	}
	// The model may drop the directory; clipboard basenames are unique, so
	// matching the basename is safe and keeps the context useful.
	if note := m.userProvidedNote("ls -l vibe_clipboard_123.png"); !strings.Contains(note, img) {
		t.Errorf("basename not matched, got %q", note)
	}
	if note := m.userProvidedNote("ls -l other.png"); note != "" {
		t.Errorf("unrelated path produced a note: %q", note)
	}
}

func TestNoteUserProvidedPath_EmptyIsIgnored(t *testing.T) {
	m := NewManager(&config.Config{})

	for _, p := range []string{"", "   "} {
		m.NoteUserProvidedPath(p)
	}

	if note := m.userProvidedNote("ls -l anything"); note != "" {
		t.Errorf("empty registrations produced a note: %q", note)
	}
}

func TestAssistedCommand_ForwardsUserProvidedContext(t *testing.T) {
	decider := &contextSafetyDecider{enabled: true}
	m := NewManager(&config.Config{YesMode: false, JevstyleModel: "jev-model", AssistedYes: true})
	m.SetSafetyDecider(decider)

	img := filepath.Join(t.TempDir(), "vibe_clipboard_999.png")
	m.NoteUserProvidedPath(img)

	ui := &notifyingUI{}
	if !m.Check("Bash", map[string]any{"command": "ls -l " + img}, ui) {
		t.Fatal("expected the command to be auto-approved")
	}
	if !decider.called {
		t.Fatal("expected the context-aware decider to be called")
	}
	if !strings.Contains(decider.note, img) {
		t.Errorf("decider note missing the user-provided path, got %q", decider.note)
	}
	if !strings.Contains(decider.note, "clipboard") {
		t.Errorf("decider note should explain the path came from the clipboard, got %q", decider.note)
	}
}

func TestAssistedCommand_NoNoteForUnrelatedPath(t *testing.T) {
	decider := &contextSafetyDecider{enabled: true}
	m := NewManager(&config.Config{YesMode: false, JevstyleModel: "jev-model", AssistedYes: true})
	m.SetSafetyDecider(decider)

	m.NoteUserProvidedPath(filepath.Join(t.TempDir(), "vibe_clipboard_999.png"))

	ui := &notifyingUI{}
	if !m.Check("Bash", map[string]any{"command": "ls -l unrelated.txt"}, ui) {
		t.Fatal("expected the command to be auto-approved")
	}
	if decider.note != "" {
		t.Errorf("unrelated command should carry no note, got %q", decider.note)
	}
}

func TestAssistedAction_ForwardsUserProvidedContext(t *testing.T) {
	decider := &contextSafetyDecider{enabled: true}
	m := NewManager(&config.Config{YesMode: false, JevstyleModel: "jev-model", AssistedYes: true})
	m.SetSafetyDecider(decider)

	img := filepath.Join(t.TempDir(), "vibe_clipboard_777.png")
	m.NoteUserProvidedPath(img)

	ui := &notifyingUI{}
	// Read is safe-tier and never consults the decider, so use a file action
	// that would otherwise prompt.
	if !m.Check("Write", map[string]any{"file_path": img, "contents": "x"}, ui) {
		t.Fatal("expected the write to be auto-approved")
	}
	if !strings.Contains(decider.note, img) {
		t.Errorf("decider note missing the user-provided path, got %q", decider.note)
	}
}

// TestAskSafety_FallsBackForLegacyDecider keeps the older deciders working: one
// that only implements AskSafety must still be used, and must not receive a
// context note it cannot understand.
func TestAskSafety_FallsBackForLegacyDecider(t *testing.T) {
	legacy := &legacyDecisionMaker{}
	decision, handled, err := askSafety(context.Background(), legacy, "Bash", "ls", "some note")
	if err != nil {
		t.Fatalf("askSafety: %v", err)
	}
	if !handled {
		t.Fatal("expected the legacy decider to handle the call")
	}
	if decision == nil {
		t.Fatal("expected a decision")
	}
	if legacy.detail != "ls" {
		t.Errorf("legacy decider detail = %q, want %q", legacy.detail, "ls")
	}
}

// TestAskSafety_ContextOnlyDeciderIsUsed covers a decider that implements only
// the context-aware variant: it must be used rather than skipped in favour of
// the binary fallback.
func TestAskSafety_ContextOnlyDeciderIsUsed(t *testing.T) {
	dec := &contextOnlyDecider{}
	decision, handled, err := askSafety(context.Background(), dec, "Bash", "ls", "note")
	if err != nil {
		t.Fatalf("askSafety: %v", err)
	}
	if !handled || decision == nil {
		t.Fatal("expected the context-aware decider to handle the call")
	}
	if dec.note != "note" {
		t.Errorf("note = %q, want %q", dec.note, "note")
	}
}

// TestAskSafety_UnsupportedDeciderNotHandled confirms a decider without any
// AskSafety support is reported as unhandled.
func TestAskSafety_UnsupportedDeciderNotHandled(t *testing.T) {
	_, handled, err := askSafety(context.Background(), &commandOnlyDecider{enabled: true}, "Bash", "ls", "")
	if err != nil {
		t.Fatalf("askSafety: %v", err)
	}
	if handled {
		t.Fatal("expected a command-only decider to be unhandled")
	}
}

// legacyDecisionMaker implements only safetyDecisionMaker.
type legacyDecisionMaker struct {
	detail string
}

func (d *legacyDecisionMaker) Enabled() bool { return true }

func (d *legacyDecisionMaker) IsCommandDangerous(context.Context, string) (bool, error) {
	return false, nil
}

func (d *legacyDecisionMaker) AskSafety(_ context.Context, _ string, detail string) (*jevstylev3.SafetyDecision, error) {
	d.detail = detail
	return &jevstylev3.SafetyDecision{Action: "allow", Confidence: 0.95}, nil
}

// contextOnlyDecider implements only safetyContextDecisionMaker.
type contextOnlyDecider struct {
	note string
}

func (d *contextOnlyDecider) Enabled() bool { return true }

func (d *contextOnlyDecider) IsCommandDangerous(context.Context, string) (bool, error) {
	return false, nil
}

func (d *contextOnlyDecider) AskSafetyWithContext(_ context.Context, _, _, note string) (*jevstylev3.SafetyDecision, error) {
	d.note = note
	return &jevstylev3.SafetyDecision{Action: "allow", Confidence: 0.95}, nil
}

func TestUserProvidedNote_IsNonEmptyAndMentionsClipboard(t *testing.T) {
	m := NewManager(&config.Config{})
	img := filepath.Join(t.TempDir(), "vibe_clipboard_4242.png")
	m.NoteUserProvidedPath(img)

	note := m.userProvidedNote("cat " + img)
	if !strings.HasPrefix(note, "The user supplied") {
		t.Errorf("unexpected note wording: %q", note)
	}
	if !strings.Contains(note, "not chosen by the agent") {
		t.Errorf("note should clarify the agent did not choose the path: %q", note)
	}
}

// Guard the notice path used by the UI: the outcome must stay "approved" for a
// context note to be a pure improvement.
func TestAssistedCommand_ContextNoteStillApproves(t *testing.T) {
	decider := &contextSafetyDecider{enabled: true}
	m := NewManager(&config.Config{YesMode: false, JevstyleModel: "jev-model", AssistedYes: true})
	m.SetSafetyDecider(decider)

	img := filepath.Join(t.TempDir(), "vibe_clipboard_1.png")
	m.NoteUserProvidedPath(img)

	ui := &notifyingUI{}
	m.Check("Bash", map[string]any{"command": "ls -l " + img}, ui)
	if outcome, ok := ui.lastOutcome(); !ok || outcome != tui.AssistedApproved {
		t.Fatalf("expected an approval notice, got %+v", ui.notices)
	}
}
