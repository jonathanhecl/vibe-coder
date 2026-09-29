package agent

import (
	"strconv"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

// resolveImageAttachments converts [image path=...] markers in outgoing user
// messages into real Ollama image payloads. Markers are produced by Read on
// image files. They stay as plain text in the session transcript (cheap to
// persist, summarize and resume) and become bytes only here, at send time.
func (a *Agent) resolveImageAttachments(msgs []ollama.Message) []ollama.Message {
	out := make([]ollama.Message, len(msgs))
	latest := -1
	for i, m := range msgs {
		if m.Role == "user" && len(vision.ParseMarkers(m.Content)) > 0 {
			latest = i
		}
	}
	for i, m := range msgs {
		out[i] = m
		if m.Role != "user" {
			continue
		}
		paths := vision.ParseMarkers(m.Content)
		if len(paths) == 0 {
			continue
		}
		content := m.Content
		if i != latest {
			// Only the newest image travels as pixels. Re-sending every past
			// image made the model answer about an older screenshot instead
			// of the one the user just sent.
			out[i].Content = replaceAllMarkers(content, "(earlier image, no longer attached)")
			continue
		}
		var images []string
		for idx, p := range paths {
			marker := vision.MarkerFor(p)
			if idx >= vision.MaxImagesPerMessage {
				content = strings.Replace(content, marker,
					"(image omitted: at most "+strconv.Itoa(vision.MaxImagesPerMessage)+" images per message)", 1)
				continue
			}
			payload, note := a.encodeAttachedImage(p)
			if note != "" {
				content = strings.Replace(content, marker, note, 1)
				continue
			}
			images = append(images, payload)
			a.paths.add(p)
			// Replace the marker with a pathless note. The image bytes are
			// already in the message; naming a file only invites the model to
			// go Read/DescribeImage a path it does not need, which is how a
			// pasted screenshot turned into PATH ERRORs and permission
			// prompts. A path the model cannot use is worse than no path.
			content = strings.Replace(content, marker, "(image attached)", 1)
		}
		out[i].Content = content
		out[i].Images = append(out[i].Images, images...)
	}
	return out
}

// encodeAttachedImage returns the base64 payload for path (cached per file
// revision), or a transcript note explaining why the image is unavailable.
func (a *Agent) encodeAttachedImage(path string) (string, string) {
	key, err := vision.CacheKey(path)
	if err != nil {
		return "", "(image unavailable: " + shortVisionError(err) + ")"
	}
	payload, err := a.imageCache().GetOrEncode(key, func() (string, error) {
		return vision.EncodeFile(path)
	})
	if err != nil {
		return "", "(image unavailable: " + shortVisionError(err) + ")"
	}
	return payload, ""
}

func (a *Agent) imageCache() *vision.Cache {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.imgCache == nil {
		a.imgCache = vision.NewCache()
	}
	return a.imgCache
}

// shortVisionError keeps transcript notes compact; details stay in logs.
func shortVisionError(err error) string {
	msg := strings.TrimSpace(err.Error())
	if len(msg) > 160 {
		msg = msg[:160] + "..."
	}
	return msg
}
