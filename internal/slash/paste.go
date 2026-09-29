package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/clipboard"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

// PasteTaskFromSlash reads the clipboard and constructs a user message
// that includes the clipboard content. It returns the message and true
// if the line is a /paste or /image command.
//
// The returned message is ready to send to the agent. For images it
// contains the vision marker; for text and files it contains the raw
// content inline.
func PasteTaskFromSlash(c *Ctx, line string) (string, bool) {
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

	content, err := clipboard.Paste(clipboardMediaDir(c))
	if err != nil {
		return fmt.Sprintf("[System Note] Failed to read clipboard: %v", err), true
	}
	noteUserProvidedPaths(c, content)

	// The image file is intentionally NOT deleted here. The marker built
	// below is a plain-text path that the agent resolves to real bytes only
	// at send time, and the transcript keeps referencing it on every later
	// turn. Deleting it now would leave the model pointing at a file that
	// never existed. The image lives in the session media dir instead and
	// goes away together with the session.
	return buildPasteMessage(userPrompt, content), true
}

// noteUserProvidedPaths tells the permission layer which files the user handed
// over themselves. Assisted mode (JEV Style) forwards this to the decision
// model so an opaque clipboard filename is not mistaken for a path the agent
// invented, which would otherwise drop the decision to "review" and prompt the
// user for a file they just pasted.
func noteUserProvidedPaths(c *Ctx, content *clipboard.Content) {
	if c == nil || c.Perm == nil || content == nil {
		return
	}
	if content.Type == clipboard.ContentImage && content.ImagePath != "" {
		c.Perm.NoteUserProvidedPath(content.ImagePath)
	}
	for _, p := range content.FilePaths {
		c.Perm.NoteUserProvidedPath(p)
	}
}

// clipboardMediaDir returns the directory that receives pasted clipboard
// images, creating it on demand. It returns "" when no session storage is
// available, which makes clipboard.Paste fall back to the OS temp dir.
func clipboardMediaDir(c *Ctx) string {
	if c == nil || c.Cfg == nil || c.Session == nil {
		return ""
	}
	dir, err := session.EnsureMediaDir(c.Cfg.SessionsDir, c.Session.ID())
	if err != nil {
		return ""
	}
	return dir
}

// buildPasteMessage constructs the user message from clipboard content.
// It is a pure function (no I/O) so it can be tested without a real
// clipboard. Any image file referenced by the message is owned by the
// caller and must stay readable for the lifetime of the session transcript.
func buildPasteMessage(userPrompt string, content *clipboard.Content) string {
	switch content.Type {
	case clipboard.ContentEmpty:
		if userPrompt != "" {
			return fmt.Sprintf("%s\n\n[System Note] The clipboard is empty or contains no supported content (text, image, or file).", userPrompt)
		}
		return "[System Note] The clipboard is empty or contains no supported content (text, image, or file)."

	case clipboard.ContentImage:
		// The marker carries the image; at send time it becomes real pixels in
		// the message's images field behind a pathless "(image attached)" note.
		// Deliberately no filename and no "look at file X" instruction: the
		// user's own prompt is the question, and naming a path only made the
		// model go Read/DescribeImage a file it did not need.
		marker := vision.MarkerFor(content.ImagePath)
		if userPrompt != "" {
			return fmt.Sprintf("%s\n\n%s", userPrompt, marker)
		}
		return marker

	case clipboard.ContentText:
		if userPrompt != "" {
			return fmt.Sprintf("%s\n\n--- clipboard ---\n%s\n--- end clipboard ---", userPrompt, content.Text)
		}
		return fmt.Sprintf("Clipboard content:\n\n%s", content.Text)

	case clipboard.ContentFile:
		paths := strings.Join(content.FilePaths, "\n")
		if userPrompt != "" {
			return fmt.Sprintf("%s\n\nFiles copied to the clipboard:\n%s\n(Use Read or Bash to access them)", userPrompt, paths)
		}
		return fmt.Sprintf("Files copied to the clipboard:\n%s\n(Use Read or Bash to access them)", paths)

	default:
		return "[System Note] Unknown clipboard content type."
	}
}
