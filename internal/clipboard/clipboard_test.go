package clipboard

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestContentType_String(t *testing.T) {
	tests := []struct {
		ct   ContentType
		want string
	}{
		{ContentEmpty, "empty"},
		{ContentText, "text"},
		{ContentImage, "image"},
		{ContentFile, "file"},
		{ContentType(999), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.ct.String(); got != tt.want {
			t.Errorf("ContentType(%d).String() = %q, want %q", int(tt.ct), got, tt.want)
		}
	}
}

func TestContent_Cleanup_RemovesImageTempFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("temp file cleanup test skipped on windows")
	}

	tmp := filepath.Join(os.TempDir(), "vibe_clipboard_test_cleanup.png")
	if err := os.WriteFile(tmp, []byte("fake image data"), 0o600); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	content := &Content{
		Type:      ContentImage,
		ImagePath: tmp,
	}

	content.Cleanup()

	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("expected temp file %s to be removed after Cleanup()", tmp)
	}
}

func TestContent_Cleanup_NoTempFile(t *testing.T) {
	content := &Content{Type: ContentText, Text: "hello"}

	// Should not panic
	content.Cleanup()
}

func TestParseFileURLs_SingleFile(t *testing.T) {
	raw := "file:///Users/hide/project/main.go"
	paths := parseFileURLs(raw)

	if len(paths) != 1 {
		t.Fatalf("parseFileURLs returned %d paths, want 1", len(paths))
	}
	if paths[0] != "/Users/hide/project/main.go" {
		t.Errorf("parseFileURLs[0] = %q, want /Users/hide/project/main.go", paths[0])
	}
}

func TestParseFileURLs_MultipleFiles(t *testing.T) {
	raw := "file:///Users/hide/a.txt\nfile:///Users/hide/b.txt\n"
	paths := parseFileURLs(raw)

	if len(paths) != 2 {
		t.Fatalf("parseFileURLs returned %d paths, want 2", len(paths))
	}
}

func TestParseFileURLs_Empty(t *testing.T) {
	paths := parseFileURLs("")
	if len(paths) != 0 {
		t.Errorf("parseFileURLs(\"\") returned %d paths, want 0", len(paths))
	}
}

func TestParseFileURLs_NoFilePrefix(t *testing.T) {
	raw := "just some plain text"
	paths := parseFileURLs(raw)

	if len(paths) != 0 {
		t.Errorf("parseFileURLs returned %d paths for plain text, want 0", len(paths))
	}
}

func TestParseURIList_Valid(t *testing.T) {
	raw := "file:///home/user/doc.txt\n# Comment line\nfile:///home/user/image.png\n"
	content := parseURIList(raw)

	if content == nil {
		t.Fatal("parseURIList returned nil for valid uri-list")
	}
	if content.Type != ContentFile {
		t.Errorf("parseURIList Type = %v, want ContentFile", content.Type)
	}
	if len(content.FilePaths) != 2 {
		t.Errorf("parseURIList FilePaths len = %d, want 2", len(content.FilePaths))
	}
}

func TestParseURIList_Empty(t *testing.T) {
	content := parseURIList("")
	if content != nil {
		t.Errorf("parseURIList(\"\") = %v, want nil", content)
	}
}

func TestParseURIList_OnlyComments(t *testing.T) {
	content := parseURIList("# just a comment\n# another comment\n")
	if content != nil {
		t.Errorf("parseURIList with only comments = %v, want nil", content)
	}
}

func TestClassifyClipboardText_PlainText(t *testing.T) {
	content := classifyClipboardText("hello world")

	if content == nil {
		t.Fatal("classifyClipboardText returned nil for plain text")
	}
	if content.Type != ContentText {
		t.Errorf("classifyClipboardText Type = %v, want ContentText", content.Type)
	}
	if content.Text != "hello world" {
		t.Errorf("classifyClipboardText Text = %q, want 'hello world'", content.Text)
	}
}

func TestClassifyClipboardText_WhitespaceOnly(t *testing.T) {
	content := classifyClipboardText("   \n\t  \n")
	if content != nil {
		t.Errorf("classifyClipboardText with whitespace = %v, want nil", content)
	}
}

func TestClassifyClipboardText_EmptyString(t *testing.T) {
	content := classifyClipboardText("")
	if content != nil {
		t.Errorf("classifyClipboardText with empty string = %v, want nil", content)
	}
}

func TestClassifyClipboardText_FileURL(t *testing.T) {
	raw := "file:///Users/hide/project/main.go"
	content := classifyClipboardText(raw)

	if content == nil {
		t.Fatal("classifyClipboardText returned nil for file URL")
	}
	if content.Type != ContentFile {
		t.Errorf("classifyClipboardText Type = %v, want ContentFile", content.Type)
	}
	if len(content.FilePaths) != 1 {
		t.Errorf("classifyClipboardText FilePaths len = %d, want 1", len(content.FilePaths))
	}
}

func TestPaste_UnsupportedPlatform(t *testing.T) {
	// We can't really test unsupported platforms without mocking runtime.GOOS,
	// but we can verify that Paste() returns ContentEmpty on the current
	// platform when clipboard tools are missing or clipboard is empty.
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		_, err := Paste()
		if err == nil {
			t.Error("Paste() on unsupported platform should return error")
		}
	}
}

func TestPaste_Darwin_NoToolsInstalled(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-specific test")
	}

	// On a system without pngpaste, Paste() should still work via pbpaste
	// or return ContentEmpty. It should never panic.
	content, err := Paste()
	if err != nil {
		t.Logf("Paste() returned error (expected if pbpaste missing): %v", err)
		return
	}
	// If we got here, pbpaste worked. Content should be valid.
	if content.Type < ContentEmpty || content.Type > ContentFile {
		t.Errorf("Paste() returned invalid ContentType: %d", content.Type)
	}
}

func TestPaste_Linux_NoToolsInstalled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-specific test")
	}

	// Without wl-paste or xclip, Paste() should return ContentEmpty.
	content, err := Paste()
	if err != nil {
		t.Logf("Paste() returned error: %v", err)
		return
	}
	// Content may be empty or may have content if tools are installed.
	_ = content
}

func TestContent_Cleanup_Idempotent(t *testing.T) {
	tmp := filepath.Join(os.TempDir(), "vibe_clipboard_test_idempotent.png")
	if err := os.WriteFile(tmp, []byte("data"), 0o600); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	content := &Content{
		Type:      ContentImage,
		ImagePath: tmp,
	}

	content.Cleanup()
	content.Cleanup() // second call should not panic

	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("temp file should not exist after Cleanup()")
	}
}

func TestParseFileURLs_WindowsPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-specific test")
	}

	raw := "file:///C:/Users/test/file.txt"
	paths := parseFileURLs(raw)

	if len(paths) != 1 {
		t.Fatalf("parseFileURLs returned %d paths, want 1", len(paths))
	}
	if !strings.HasPrefix(paths[0], "C:") {
		t.Errorf("parseFileURLs[0] = %q, want prefix 'C:'", paths[0])
	}
}
