// Package clipboard provides cross-platform access to the system clipboard
// for text, images, and file references.
package clipboard

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ContentType classifies what is currently in the clipboard.
type ContentType int

const (
	// ContentEmpty means the clipboard is empty or inaccessible.
	ContentEmpty ContentType = iota
	// ContentText is plain text (code, errors, URLs, etc.).
	ContentText
	// ContentImage is a PNG or JPEG image.
	ContentImage
	// ContentFile is one or more file references (copied from a file manager).
	ContentFile
)

// String returns a human-readable label for the content type.
func (t ContentType) String() string {
	switch t {
	case ContentEmpty:
		return "empty"
	case ContentText:
		return "text"
	case ContentImage:
		return "image"
	case ContentFile:
		return "file"
	default:
		return "unknown"
	}
}

// Content is the extracted clipboard payload.
type Content struct {
	Type ContentType
	Text string // populated for ContentText
	// ImagePath is the absolute path of the extracted image for
	// ContentImage. The file is owned by the caller: Paste never deletes
	// it, because the transcript keeps referencing this path long after
	// the turn that produced it.
	ImagePath string
	FilePaths []string // populated for ContentFile
	MIME      string
}

// Paste inspects the system clipboard and returns its content classified
// by type. It never panics; on any error it returns ContentEmpty with a
// descriptive error wrapped in a fmt.Errorf.
//
// An extracted image is written into destDir so the caller controls the
// file's lifetime. When destDir is empty the OS temp dir is used instead;
// callers that keep the image in a session must pass a real directory
// because the temp dir may be reclaimed before the agent reads the path.
func Paste(destDir string) (*Content, error) {
	switch runtime.GOOS {
	case "darwin":
		return pasteDarwin(destDir)
	case "linux":
		return pasteLinux(destDir)
	case "windows":
		return pasteWindows(destDir)
	default:
		return nil, fmt.Errorf("clipboard access is not supported on %s", runtime.GOOS)
	}
}

// imagePath returns a unique PNG path inside dir, falling back to the OS
// temp dir when dir is empty or unusable.
func imagePath(dir string) string {
	base := fmt.Sprintf("vibe_clipboard_%d.png", time.Now().UnixNano())
	if strings.TrimSpace(dir) == "" {
		return filepath.Join(os.TempDir(), base)
	}
	return filepath.Join(dir, base)
}

// pasteDarwin reads the clipboard on macOS.
// It first tries pngpaste for images, then falls back to pbpaste for text
// and file URLs.
func pasteDarwin(destDir string) (*Content, error) {
	// 1. Try image via pngpaste
	if imgPath, err := darwinPasteImage(destDir); err == nil && imgPath != "" {
		return &Content{
			Type:      ContentImage,
			ImagePath: imgPath,
			MIME:      "image/png",
		}, nil
	}

	// 2. Read raw clipboard text
	raw, err := exec.Command("pbpaste").Output()
	if err != nil {
		return &Content{Type: ContentEmpty}, fmt.Errorf("pbpaste failed: %w", err)
	}

	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return &Content{Type: ContentEmpty}, nil
	}

	// 3. Check for file:// URLs (copied from Finder)
	if paths := parseFileURLs(trimmed); len(paths) > 0 {
		return &Content{
			Type:      ContentFile,
			FilePaths: paths,
			Text:      trimmed,
		}, nil
	}

	// 4. Plain text
	return &Content{
		Type: ContentText,
		Text: trimmed,
		MIME: "text/plain",
	}, nil
}

// darwinPasteImage attempts to extract a PNG image from the clipboard using
// pngpaste. Returns empty string if no image is present or pngpaste is missing.
// The file is written into destDir and left there for the caller to clean up.
func darwinPasteImage(destDir string) (string, error) {
	tmp := imagePath(destDir)
	cmd := exec.Command("pngpaste", tmp)
	if err := cmd.Run(); err != nil {
		// pngpaste not installed or no image in clipboard — not an error,
		// just a signal to try text instead.
		return "", nil
	}
	// Verify the file was actually written
	if info, err := os.Stat(tmp); err != nil || info.Size() == 0 {
		_ = os.Remove(tmp)
		return "", nil
	}
	return tmp, nil
}

// pasteLinux reads the clipboard on Linux, trying Wayland (wl-paste) first,
// then X11 (xclip).
func pasteLinux(destDir string) (*Content, error) {
	// 1. Try Wayland image
	if imgPath, ok := linuxPasteImage(destDir, "wl-paste", "--type", "image/png"); ok {
		return &Content{
			Type:      ContentImage,
			ImagePath: imgPath,
			MIME:      "image/png",
		}, nil
	}

	// 2. Try X11 image
	if imgPath, ok := linuxPasteImage(destDir, "xclip", "-selection", "clipboard", "-t", "image/png", "-o"); ok {
		return &Content{
			Type:      ContentImage,
			ImagePath: imgPath,
			MIME:      "image/png",
		}, nil
	}

	// 3. Try Wayland text
	if raw, err := exec.Command("wl-paste", "--type", "text/plain").Output(); err == nil {
		if content := classifyClipboardText(string(raw)); content != nil {
			return content, nil
		}
	}

	// 4. Try X11 text
	if raw, err := exec.Command("xclip", "-selection", "clipboard", "-t", "text/plain", "-o").Output(); err == nil {
		if content := classifyClipboardText(string(raw)); content != nil {
			return content, nil
		}
	}

	// 5. Try file list (Wayland)
	if raw, err := exec.Command("wl-paste", "--type", "text/uri-list").Output(); err == nil {
		if content := parseURIList(string(raw)); content != nil {
			return content, nil
		}
	}

	// 6. Try file list (X11)
	if raw, err := exec.Command("xclip", "-selection", "clipboard", "-t", "text/uri-list", "-o").Output(); err == nil {
		if content := parseURIList(string(raw)); content != nil {
			return content, nil
		}
	}

	return &Content{Type: ContentEmpty}, nil
}

// linuxPasteImage tries to extract an image using the given command.
// The command must write image bytes to stdout (wl-paste, xclip).
// The image is written into destDir and left there for the caller to clean
// up. Returns the file path and true on success.
func linuxPasteImage(destDir string, name string, args ...string) (string, bool) {
	tmp := imagePath(destDir)
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return "", false
	}
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return "", false
	}
	return tmp, true
}

// pasteWindows reads the clipboard on Windows using PowerShell.
func pasteWindows(destDir string) (*Content, error) {
	// 1. Try image via PowerShell. Prefer destDir so the caller owns the
	// file lifetime, but fall back to %TEMP% when it is empty.
	dir := strings.TrimSpace(destDir)
	if dir == "" {
		dir = os.Getenv("TEMP")
	}
	if trimmed := strings.TrimSpace(dir); trimmed != "" {
		psImg := fmt.Sprintf(`$img = Get-Clipboard -Format Image; if ($img) { $path = Join-Path '%s' ('vibe_clipboard_' + [DateTime]::Now.Ticks + '.png'); $img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); Write-Output $path }`, strings.ReplaceAll(trimmed, "'", "''"))
		if out, err := exec.Command("powershell", "-NoProfile", "-Command", psImg).Output(); err == nil {
			trimmedOut := strings.TrimSpace(string(out))
			if trimmedOut != "" && !strings.Contains(trimmedOut, "Get-Clipboard") {
				// Validate the path exists
				if info, statErr := os.Stat(trimmedOut); statErr == nil && info.Size() > 0 {
					return &Content{
						Type:      ContentImage,
						ImagePath: trimmedOut,
						MIME:      "image/png",
					}, nil
				}
			}
		}
	}

	// 2. Try file list
	psFiles := `Get-Clipboard -Format FileDropList | ForEach-Object { Write-Output $_.FullName }`
	if out, err := exec.Command("powershell", "-NoProfile", "-Command", psFiles).Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		var paths []string
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l != "" {
				paths = append(paths, l)
			}
		}
		if len(paths) > 0 {
			return &Content{
				Type:      ContentFile,
				FilePaths: paths,
				Text:      strings.Join(paths, "\n"),
			}, nil
		}
	}

	// 3. Plain text
	if out, err := exec.Command("powershell", "-NoProfile", "-Command", "Get-Clipboard -Format Text").Output(); err == nil {
		if content := classifyClipboardText(string(out)); content != nil {
			return content, nil
		}
	}

	return &Content{Type: ContentEmpty}, nil
}

// classifyClipboardText inspects raw clipboard text and returns a Content
// if it is non-empty and not just whitespace.
func classifyClipboardText(raw string) *Content {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	// Check for file:// URLs
	if paths := parseFileURLs(trimmed); len(paths) > 0 {
		return &Content{
			Type:      ContentFile,
			FilePaths: paths,
			Text:      trimmed,
		}
	}
	return &Content{
		Type: ContentText,
		Text: trimmed,
		MIME: "text/plain",
	}
}

// parseFileURLs extracts local file paths from file:// URL strings.
func parseFileURLs(raw string) []string {
	var paths []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "file://") {
			if u, err := url.Parse(line); err == nil {
				path := u.Path
				// On macOS/Linux, url.Path is already clean.
				// On Windows, strip the leading slash before the drive letter.
				if runtime.GOOS == "windows" && len(path) > 2 && path[0] == '/' && path[2] == ':' {
					path = path[1:]
				}
				paths = append(paths, path)
			}
		} else if _, err := os.Stat(line); err == nil {
			// Plain path that exists on disk
			paths = append(paths, line)
		}
	}
	return paths
}

// parseURIList parses a text/uri-list (standard Linux clipboard format).
func parseURIList(raw string) *Content {
	lines := strings.Split(raw, "\n")
	var paths []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "file://") {
			if u, err := url.Parse(line); err == nil {
				path := u.Path
				if runtime.GOOS == "windows" && len(path) > 2 && path[0] == '/' && path[2] == ':' {
					path = path[1:]
				}
				paths = append(paths, path)
			}
		}
	}
	if len(paths) == 0 {
		return nil
	}
	return &Content{
		Type:      ContentFile,
		FilePaths: paths,
		Text:      strings.Join(paths, "\n"),
	}
}
