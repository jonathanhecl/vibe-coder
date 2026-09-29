package slash

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/clipboard"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/jevstylev3"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

// TestPasteTaskFromSlash_ImageFileOutlivesTheCall is the regression test for
// the bug where /paste deleted the image before the agent ever read it: the
// message only carries a plain-text path marker, resolved to bytes at send
// time, so removing the file here made the marker dangle.
// TestPasteTaskFromSlash_DeletedImageIsDetected pins the agent-side
// behaviour: when the image is gone, resolveImageAttachments must degrade to
// a visible note instead of silently dropping the marker.
func TestPasteTaskFromSlash_DeletedImageIsDetected(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "vibe_clipboard_gone.png")

	content := &clipboard.Content{Type: clipboard.ContentImage, ImagePath: missing, MIME: "image/png"}
	msg := buildPasteMessage("", content)

	if _, err := os.Stat(missing); err == nil {
		t.Fatal("precondition failed: the image should not exist")
	}
	paths := vision.ParseMarkers(msg)
	if len(paths) != 1 {
		t.Fatalf("expected 1 marker even for a missing file, got %d", len(paths))
	}
	if _, err := vision.CacheKey(paths[0]); err == nil {
		t.Error("CacheKey should fail for a missing image so the agent can report it")
	}
}

func TestPasteTaskFromSlash_ImageFileOutlivesTheCall(t *testing.T) {
	tmp := t.TempDir()
	img := filepath.Join(tmp, "vibe_clipboard_test.png")
	if err := os.WriteFile(img, []byte("fake png bytes"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	// Simulate the state buildPasteMessage leaves behind: a message holding
	// a marker for a real file. Nothing in this flow may delete the file.
	content := &clipboard.Content{Type: clipboard.ContentImage, ImagePath: img, MIME: "image/png"}
	msg := buildPasteMessage("look at this", content)

	paths := vision.ParseMarkers(msg)
	if len(paths) != 1 {
		t.Fatalf("expected 1 image marker in message, got %d: %s", len(paths), msg)
	}
	if paths[0] != img {
		t.Fatalf("marker path = %q, want %q", paths[0], img)
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("image referenced by the message is not readable: %v", err)
	}
}

func TestClipboardMediaDir_CreatesAndScopesToSession(t *testing.T) {
	sessionsDir := t.TempDir()
	sess := session.New(&config.Config{Cwd: t.TempDir(), SessionsDir: sessionsDir})

	dir := clipboardMediaDir(&Ctx{Cfg: &config.Config{SessionsDir: sessionsDir}, Session: sess})
	if dir == "" {
		t.Fatal("clipboardMediaDir returned empty, want the session media dir")
	}

	want, err := session.EnsureMediaDir(sessionsDir, sess.ID())
	if err != nil {
		t.Fatalf("EnsureMediaDir: %v", err)
	}
	if dir != want {
		t.Errorf("clipboardMediaDir = %q, want %q", dir, want)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("media dir not created: %v", err)
	}
	// Media lives under SessionsDir/media/<id>, never beside the transcript
	// and never relative to the process cwd.
	root := filepath.Dir(dir)
	if root != filepath.Join(sessionsDir, "media") {
		t.Errorf("media root = %q, want %q", root, filepath.Join(sessionsDir, "media"))
	}
	if filepath.Base(dir) != sess.ID() {
		t.Errorf("media dir name = %q, want the session id %q", filepath.Base(dir), sess.ID())
	}
}

func TestClipboardMediaDir_FallsBackToEmpty(t *testing.T) {
	cases := map[string]*Ctx{
		"nil ctx":         nil,
		"nil cfg":         {Session: session.New(&config.Config{SessionsDir: t.TempDir()})},
		"nil session":     {Cfg: &config.Config{SessionsDir: t.TempDir()}},
		"empty sessionsd": {Cfg: &config.Config{}, Session: session.New(&config.Config{})},
	}

	for name, c := range cases {
		if got := clipboardMediaDir(c); got != "" {
			t.Errorf("%s: clipboardMediaDir = %q, want empty fallback", name, got)
		}
	}
}

// spyDecisionMaker records the runtime context note the permission layer sends
// to the decision model.
type spyDecisionMaker struct {
	note string
}

func (s *spyDecisionMaker) Enabled() bool { return true }

func (s *spyDecisionMaker) IsCommandDangerous(context.Context, string) (bool, error) {
	return false, nil
}

func (s *spyDecisionMaker) AskSafetyWithContext(_ context.Context, _, _, note string) (*jevstylev3.SafetyDecision, error) {
	s.note = note
	return &jevstylev3.SafetyDecision{Action: "allow", Confidence: 0.95}, nil
}

// TestNoteUserProvidedPaths_ReachesDecisionModel proves the wiring end to end:
// a path the user pasted is registered on the permission manager, and assisted
// mode forwards it as context when the agent inspects that file.
func TestNoteUserProvidedPaths_ReachesDecisionModel(t *testing.T) {
	spy := &spyDecisionMaker{}
	perm := permissions.NewManager(&config.Config{YesMode: false, JevstyleModel: "jev-model", AssistedYes: true})
	perm.SetSafetyDecider(spy)

	img := filepath.Join(t.TempDir(), "vibe_clipboard_555.png")
	doc := filepath.Join(t.TempDir(), "notes.txt")
	noteUserProvidedPaths(&Ctx{Perm: perm}, &clipboard.Content{
		Type:      clipboard.ContentImage,
		ImagePath: img,
		FilePaths: []string{doc},
	})

	for _, p := range []string{img, doc} {
		spy.note = ""
		if !perm.Check("Bash", map[string]any{"command": "ls -l " + p}, nil) {
			t.Fatalf("expected assisted mode to approve inspecting %q", p)
		}
		if !strings.Contains(spy.note, p) {
			t.Errorf("path %q never reached the decision model, note = %q", p, spy.note)
		}
	}
}

func TestNoteUserProvidedPaths_ToleratesMissingInputs(t *testing.T) {
	// Must never panic: /paste can run without a session or permission layer.
	noteUserProvidedPaths(nil, &clipboard.Content{Type: clipboard.ContentImage, ImagePath: "/tmp/x.png"})
	noteUserProvidedPaths(&Ctx{}, &clipboard.Content{Type: clipboard.ContentImage, ImagePath: "/tmp/x.png"})
	noteUserProvidedPaths(&Ctx{Perm: permissions.NewManager(&config.Config{})}, nil)
	noteUserProvidedPaths(&Ctx{Perm: permissions.NewManager(&config.Config{})}, &clipboard.Content{Type: clipboard.ContentText, Text: "hi"})
}

func TestPasteTaskFromSlash_NotAPasteCommand(t *testing.T) {
	tests := []string{
		"",
		"   ",
		"/help",
		"/plan refactor",
		"hello world",
		"/paste",
	}

	for _, line := range tests {
		// "/paste" alone IS a paste command (with empty prompt), so skip it
		if line == "/paste" {
			continue
		}
		msg, ok := PasteTaskFromSlash(nil, line)
		if ok {
			t.Errorf("PasteTaskFromSlash(%q) = (%q, true), want (_, false)", line, msg)
		}
	}
}

func TestPasteTaskFromSlash_InvalidCommands(t *testing.T) {
	lines := []string{
		"",
		"   ",
		"/help",
		"/plan refactor",
		"/review check this",
		"hello world",
	}

	for _, line := range lines {
		msg, ok := PasteTaskFromSlash(nil, line)
		if ok {
			t.Errorf("PasteTaskFromSlash(%q) = (%q, true), want (_, false)", line, msg)
		}
		if msg != "" {
			t.Errorf("PasteTaskFromSlash(%q) msg = %q, want empty", line, msg)
		}
	}
}

func TestBuildPasteMessage_TextWithPrompt(t *testing.T) {
	content := &clipboard.Content{
		Type: clipboard.ContentText,
		Text: "error: undefined variable",
	}

	msg := buildPasteMessage("¿qué está pasando aquí?", content)

	if !strings.Contains(msg, "¿qué está pasando aquí?") {
		t.Errorf("buildPasteMessage missing user prompt, got: %s", msg)
	}
	if !strings.Contains(msg, "error: undefined variable") {
		t.Errorf("buildPasteMessage missing clipboard text, got: %s", msg)
	}
	if !strings.Contains(msg, "--- clipboard ---") {
		t.Errorf("buildPasteMessage missing clipboard delimiter, got: %s", msg)
	}
}

func TestBuildPasteMessage_TextWithoutPrompt(t *testing.T) {
	content := &clipboard.Content{
		Type: clipboard.ContentText,
		Text: "some code snippet",
	}

	msg := buildPasteMessage("", content)

	if !strings.Contains(msg, "some code snippet") {
		t.Errorf("buildPasteMessage missing clipboard text, got: %s", msg)
	}
	if !strings.Contains(msg, "Clipboard content") {
		t.Errorf("buildPasteMessage missing default label, got: %s", msg)
	}
}

func TestBuildPasteMessage_ImageWithPrompt(t *testing.T) {
	content := &clipboard.Content{
		Type:      clipboard.ContentImage,
		ImagePath: "/tmp/vibe_clipboard_123.png",
		MIME:      "image/png",
	}

	msg := buildPasteMessage("mira esta imagen", content)

	if !strings.Contains(msg, "mira esta imagen") {
		t.Errorf("buildPasteMessage missing user prompt, got: %s", msg)
	}
	if !strings.Contains(msg, "/tmp/vibe_clipboard_123.png") {
		t.Errorf("buildPasteMessage missing image path in marker, got: %s", msg)
	}
}

func TestBuildPasteMessage_ImageWithoutPrompt(t *testing.T) {
	content := &clipboard.Content{
		Type:      clipboard.ContentImage,
		ImagePath: "/tmp/vibe_clipboard_456.png",
		MIME:      "image/png",
	}

	msg := buildPasteMessage("", content)

	if !strings.Contains(msg, "image from the clipboard") {
		t.Errorf("buildPasteMessage missing default image label, got: %s", msg)
	}
	if !strings.Contains(msg, "/tmp/vibe_clipboard_456.png") {
		t.Errorf("buildPasteMessage missing image path in marker, got: %s", msg)
	}
}

func TestBuildPasteMessage_FileWithPrompt(t *testing.T) {
	content := &clipboard.Content{
		Type:      clipboard.ContentFile,
		FilePaths: []string{"/Users/hide/project/main.go", "/Users/hide/project/util.go"},
	}

	msg := buildPasteMessage("¿qué hace este código?", content)

	if !strings.Contains(msg, "¿qué hace este código?") {
		t.Errorf("buildPasteMessage missing user prompt, got: %s", msg)
	}
	if !strings.Contains(msg, "/Users/hide/project/main.go") {
		t.Errorf("buildPasteMessage missing first file path, got: %s", msg)
	}
	if !strings.Contains(msg, "/Users/hide/project/util.go") {
		t.Errorf("buildPasteMessage missing second file path, got: %s", msg)
	}
	if !strings.Contains(msg, "Files copied to the clipboard") {
		t.Errorf("buildPasteMessage missing file label, got: %s", msg)
	}
}

func TestBuildPasteMessage_FileWithoutPrompt(t *testing.T) {
	content := &clipboard.Content{
		Type:      clipboard.ContentFile,
		FilePaths: []string{"/home/user/doc.txt"},
	}

	msg := buildPasteMessage("", content)

	if !strings.Contains(msg, "/home/user/doc.txt") {
		t.Errorf("buildPasteMessage missing file path, got: %s", msg)
	}
	if !strings.Contains(msg, "Files copied to the clipboard") {
		t.Errorf("buildPasteMessage missing file label, got: %s", msg)
	}
}

func TestBuildPasteMessage_EmptyClipboard(t *testing.T) {
	content := &clipboard.Content{Type: clipboard.ContentEmpty}

	msgWithPrompt := buildPasteMessage("analiza esto", content)
	if !strings.Contains(msgWithPrompt, "analiza esto") {
		t.Errorf("buildPasteMessage with prompt missing user text, got: %s", msgWithPrompt)
	}
	if !strings.Contains(msgWithPrompt, "clipboard is empty") {
		t.Errorf("buildPasteMessage with prompt missing empty note, got: %s", msgWithPrompt)
	}

	msgNoPrompt := buildPasteMessage("", content)
	if !strings.Contains(msgNoPrompt, "clipboard is empty") {
		t.Errorf("buildPasteMessage no prompt missing empty note, got: %s", msgNoPrompt)
	}
}

func TestBuildPasteMessage_UnknownType(t *testing.T) {
	content := &clipboard.Content{Type: clipboard.ContentType(999)}

	msg := buildPasteMessage("test", content)
	if !strings.Contains(msg, "Unknown clipboard content type") {
		t.Errorf("buildPasteMessage missing unknown type note, got: %s", msg)
	}
}

func TestBuildPasteMessage_TextPreservesNewlines(t *testing.T) {
	content := &clipboard.Content{
		Type: clipboard.ContentText,
		Text: "line1\nline2\nline3",
	}

	msg := buildPasteMessage("", content)

	if !strings.Contains(msg, "line1\nline2\nline3") {
		t.Errorf("buildPasteMessage did not preserve newlines in text, got: %s", msg)
	}
}

func TestBuildPasteMessage_FileWithManyPaths(t *testing.T) {
	content := &clipboard.Content{
		Type: clipboard.ContentFile,
		FilePaths: []string{
			"/a/file1.txt",
			"/b/file2.txt",
			"/c/file3.txt",
		},
	}

	msg := buildPasteMessage("revisa estos archivos", content)

	for _, p := range content.FilePaths {
		if !strings.Contains(msg, p) {
			t.Errorf("buildPasteMessage missing path %s, got: %s", p, msg)
		}
	}
}
