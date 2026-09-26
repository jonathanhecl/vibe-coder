package slash

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/clipboard"
)

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
		msg, ok := PasteTaskFromSlash(line)
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
		msg, ok := PasteTaskFromSlash(line)
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
	if !strings.Contains(msg, "Contenido del clipboard") {
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

	if !strings.Contains(msg, "imagen desde el clipboard") {
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
	if !strings.Contains(msg, "Archivos copiados al clipboard") {
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
	if !strings.Contains(msg, "Archivos copiados al clipboard") {
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
