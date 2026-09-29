package agent

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

// pastedImageRe matches clipboard image files created by /image and /paste,
// optionally preceded by a directory.
var pastedImageRe = regexp.MustCompile(`(?:[^\s"'` + "`" + `<>]*[/\\])?vibe_clipboard_\d+\.png`)

const pastedImagePlaceholder = "(pasted image)"

func isPastedImageName(p string) bool {
	return pastedImageRe.MatchString(filepath.Base(strings.ReplaceAll(p, `\`, "/")))
}

// setLastImage remembers the newest image attached to a user message.
func (a *Agent) setLastImage(msgs []ollama.Message) {
	latest := ""
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		if paths := vision.ParseMarkers(m.Content); len(paths) > 0 {
			latest = paths[len(paths)-1]
		}
	}
	if latest == "" {
		return
	}
	a.mu.Lock()
	a.lastImage = latest
	a.mu.Unlock()
}

func (a *Agent) newestImage() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastImage
}

// scrubPastedImagePaths hides clipboard image filenames from the history sent
// to the model. Pasted images travel as pixels; a leftover filename from an
// older turn only tempts the model to call Read/DescribeImage on a stale path.
func scrubPastedImagePaths(msgs []ollama.Message) []ollama.Message {
	for i, m := range msgs {
		if m.Role == "system" {
			continue
		}
		msgs[i].Content = pastedImageRe.ReplaceAllString(m.Content, pastedImagePlaceholder)
		if len(m.ToolCalls) == 0 {
			continue
		}
		calls := make([]ollama.MessageToolCall, len(m.ToolCalls))
		for j, c := range m.ToolCalls {
			calls[j] = c
			args := make(map[string]any, len(c.Function.Arguments))
			for k, v := range c.Function.Arguments {
				if s, ok := v.(string); ok {
					v = pastedImageRe.ReplaceAllString(s, pastedImagePlaceholder)
				}
				args[k] = v
			}
			calls[j].Function.Arguments = args
		}
		msgs[i].ToolCalls = calls
	}
	return msgs
}
