package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/clipboard"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

// pasteTaskFromSlash reads the clipboard and constructs a user message
// that includes the clipboard content. It returns the message and true
// if the line is a /paste or /image command.
//
// The returned message is ready to send to the agent. For images it
// contains the vision marker; for text and files it contains the raw
// content inline.
func PasteTaskFromSlash(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return "", false
	}

	cmd := strings.ToLower(fields[0])
	if cmd != "/paste" && cmd != "/image" && cmd != "paste" && cmd != "image" {
		return "", false
	}

	// Everything after the command is the user's prompt.
	userPrompt := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))

	content, err := clipboard.Paste()
	if err != nil {
		return fmt.Sprintf("[System Note] Failed to read clipboard: %v", err), true
	}
	defer content.Cleanup()

	switch content.Type {
	case clipboard.ContentEmpty:
		if userPrompt != "" {
			return fmt.Sprintf("%s\n\n[System Note] The clipboard is empty or contains no supported content (text, image, or file).", userPrompt), true
		}
		return "[System Note] The clipboard is empty or contains no supported content (text, image, or file).", true

	case clipboard.ContentImage:
		marker := vision.MarkerFor(content.ImagePath)
		if userPrompt != "" {
			return fmt.Sprintf("%s\n\nHe pegado una imagen desde el clipboard. %s", userPrompt, marker), true
		}
		return fmt.Sprintf("He pegado una imagen desde el clipboard. Analiza su contenido.\n%s", marker), true

	case clipboard.ContentText:
		if userPrompt != "" {
			return fmt.Sprintf("%s\n\n--- clipboard ---\n%s\n--- end clipboard ---", userPrompt, content.Text), true
		}
		return fmt.Sprintf("Contenido del clipboard:\n\n%s", content.Text), true

	case clipboard.ContentFile:
		paths := strings.Join(content.FilePaths, "\n")
		if userPrompt != "" {
			return fmt.Sprintf("%s\n\nArchivos copiados al clipboard:\n%s\n(Usa Read o Bash para acceder a ellos)", userPrompt, paths), true
		}
		return fmt.Sprintf("Archivos copiados al clipboard:\n%s\n(Usa Read o Bash para acceder a ellos)", paths), true

	default:
		return "[System Note] Unknown clipboard content type.", true
	}
}

// isPasteCommand reports whether the line is a /paste or /image command.
func isPasteCommand(line string) bool {
	trimmed := strings.TrimSpace(line)
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return false
	}
	cmd := strings.ToLower(fields[0])
	return cmd == "/paste" || cmd == "/image" || cmd == "paste" || cmd == "image"
}
