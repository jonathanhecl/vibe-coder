package agent

import (
	"context"
	"strconv"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

// resolveOutgoingImages converts [image path=...] markers in outgoing user
// messages according to who can actually see:
//
//   - an explicitly bound vision role (/setmodel vision): substitute its
//     description of the image.
//   - main model with (or possibly with) vision: attach real image bytes.
//   - main model known without vision + vision sidecar: substitute a cached
//     sidecar-generated description ("borrowed vision").
//   - neither: leave an honest unavailable note naming both models.
//
// Markers stay as plain text in the transcript in all cases.
func (a *Agent) resolveOutgoingImages(ctx context.Context, msgs []ollama.Message) []ollama.Message {
	if a.visionRoleMaySee() {
		return a.resolveViaVisionRole(ctx, msgs)
	}
	if a.mainMaySee() {
		return a.resolveImageAttachments(msgs)
	}
	if a.sidecarMaySee() {
		return a.resolveViaSidecar(ctx, msgs)
	}
	out := make([]ollama.Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		if m.Role != "user" || len(vision.ParseMarkers(m.Content)) == 0 {
			continue
		}
		out[i].Content = replaceAllMarkers(m.Content,
			"(image unavailable: neither "+a.cfg.Model+" nor the sidecar can see images; switch to a vision-capable model with /setmodel)")
	}
	return out
}

func replaceAllMarkers(content, replacement string) string {
	paths := vision.ParseMarkers(content)
	for _, p := range paths {
		content = strings.Replace(content, vision.MarkerFor(p), replacement, 1)
	}
	return content
}

func (a *Agent) mainMaySee() bool {
	return a.cfg != nil && (a.cfg.VisionAvailable || !a.cfg.VisionKnown)
}

// visionRoleMaySee reports whether a model is explicitly bound to the vision
// role. Such a binding wins over the main model and the sidecar: the user
// asked for that model to be the eyes.
func (a *Agent) visionRoleMaySee() bool {
	a.mu.RLock()
	v := a.visionRole
	a.mu.RUnlock()
	return v.Enabled()
}

// resolveViaVisionRole substitutes each marker with the vision model's
// description of the image. Failures degrade to honest per-file notes.
func (a *Agent) resolveViaVisionRole(ctx context.Context, msgs []ollama.Message) []ollama.Message {
	out := make([]ollama.Message, len(msgs))
	needed := false
	for _, m := range msgs {
		if m.Role == "user" && len(vision.ParseMarkers(m.Content)) > 0 {
			needed = true
			break
		}
	}
	if needed {
		a.ui.StartWaiting("reading image via " + shortModelName(a.visionRoleName()) + "…")
		defer a.ui.StopWaiting()
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
		for idx, p := range paths {
			marker := vision.MarkerFor(p)
			if idx >= vision.MaxImagesPerMessage {
				content = strings.Replace(content, marker,
					"(image omitted: at most "+strconv.Itoa(vision.MaxImagesPerMessage)+" images per message)", 1)
				continue
			}
			desc, note := a.describeViaVisionRole(ctx, p)
			if note != "" {
				content = strings.Replace(content, marker, note, 1)
				continue
			}
			content = strings.Replace(content, marker,
				"[vision-role]\n"+desc+"\n[/vision-role]", 1)
		}
		out[i].Content = content
	}
	return out
}

// visionRoleName is the bound vision model name, or "" when unbound.
func (a *Agent) visionRoleName() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.visionRole.Model()
}

// describeViaVisionRole returns the cached vision-role description for path,
// or a transcript note when the look failed.
func (a *Agent) describeViaVisionRole(ctx context.Context, path string) (string, string) {
	revision, err := vision.CacheKey(path)
	if err != nil {
		return "", "(image unavailable: " + shortVisionError(err) + ")"
	}
	key := "visionrole|" + revision
	if cached, ok := a.descCache().Get(key); ok {
		return cached, ""
	}
	a.mu.RLock()
	v := a.visionRole
	a.mu.RUnlock()
	desc, err := v.Describe(ctx, path, "")
	if err != nil {
		return "", "(vision role failed: " + shortVisionError(err) + ")"
	}
	a.descCache().Put(key, desc)
	return desc, ""
}

// sidecarMaySee reports whether borrowed vision is worth trying: the pool
// is enabled and the sidecar is known-capable (or its capability is
// unknown, in which case the attempt itself is the honest answer).
func (a *Agent) sidecarMaySee() bool {
	if a.cfg == nil {
		return false
	}
	a.mu.RLock()
	side := a.side
	a.mu.RUnlock()
	if side == nil || !side.Enabled() {
		return false
	}
	return a.cfg.SidecarVisionAvailable || !a.cfg.SidecarVisionKnown
}

// resolveViaSidecar substitutes each marker with a cached sidecar
// description of the image. Failures degrade to honest per-file notes.
func (a *Agent) resolveViaSidecar(ctx context.Context, msgs []ollama.Message) []ollama.Message {
	out := make([]ollama.Message, len(msgs))
	needed := false
	for _, m := range msgs {
		if m.Role == "user" && len(vision.ParseMarkers(m.Content)) > 0 {
			needed = true
			break
		}
	}
	if needed {
		a.ui.StartWaiting("reading image via " + shortModelName(a.cfg.SidecarModel) + "…")
		defer a.ui.StopWaiting()
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
		for idx, p := range paths {
			marker := vision.MarkerFor(p)
			if idx >= vision.MaxImagesPerMessage {
				content = strings.Replace(content, marker,
					"(image omitted: at most "+strconv.Itoa(vision.MaxImagesPerMessage)+" images per message)", 1)
				continue
			}
			desc, note := a.describeViaSidecar(ctx, p)
			if note != "" {
				content = strings.Replace(content, marker, note, 1)
				continue
			}
			content = strings.Replace(content, marker,
				"[sidecar-vision]\n"+desc+"\n[/sidecar-vision]", 1)
		}
		out[i].Content = content
	}
	return out
}

// describeViaSidecar returns the cached sidecar description for path, or a
// transcript note when the look failed.
func (a *Agent) describeViaSidecar(ctx context.Context, path string) (string, string) {
	revision, err := vision.CacheKey(path)
	if err != nil {
		return "", "(image unavailable: " + shortVisionError(err) + ")"
	}
	key := "desc|" + revision
	if cached, ok := a.descCache().Get(key); ok {
		return cached, ""
	}
	a.mu.RLock()
	side := a.side
	a.mu.RUnlock()
	if side == nil || !side.Enabled() {
		return "", "(image unavailable: sidecar is not enabled)"
	}
	desc, err := side.DescribeImage(ctx, path, "")
	if err != nil {
		return "", "(sidecar vision failed: " + shortVisionError(err) + ")"
	}
	a.descCache().Put(key, desc)
	return desc, ""
}

func (a *Agent) descCache() *vision.Cache {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.descCacheStore == nil {
		a.descCacheStore = vision.NewCache()
	}
	return a.descCacheStore
}
