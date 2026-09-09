package agent

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

func newVisionTestAgent(t *testing.T) *Agent {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:           "llava",
		ContextWindow:   8000,
		MaxTokens:       32,
		Temperature:     0.1,
		Cwd:             tmp,
		SessionsDir:     tmp,
		VisionAvailable: true,
		VisionKnown:     true,
	}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	return New(cfg, fakeClient{}, reg, perm, sess, &fakeUI{})
}

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 8), uint8(y * 8), 200, 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

func TestResolveImageAttachmentsAttaches(t *testing.T) {
	t.Parallel()
	ag := newVisionTestAgent(t)
	photo := filepath.Join(t.TempDir(), "photo.png")
	writeTestPNG(t, photo)

	ag.sess.AddUser("describe this")
	ag.sess.AddToolObservation("Read", vision.MarkerFor(photo))
	msgs := ag.buildOllamaMessages("SYSTEM")
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	last := msgs[2]
	if len(last.Images) != 1 || last.Images[0] == "" {
		t.Fatalf("expected one attached image, got %+v", last.Images)
	}
	if strings.Contains(last.Content, "[image path=") {
		t.Fatalf("expected marker to be replaced, got %q", last.Content)
	}
	if !strings.Contains(last.Content, "(image attached: photo.png)") {
		t.Fatalf("expected attached note, got %q", last.Content)
	}
	if ag.imgCache == nil || ag.imageCache().Size() != 1 {
		t.Fatal("expected the encoded image to be cached")
	}

	// A second turn reuses the cache instead of re-encoding.
	again := ag.resolveImageAttachments([]ollama.Message{{Role: "user", Content: vision.MarkerFor(photo)}})
	if len(again[0].Images) != 1 || again[0].Images[0] != last.Images[0] {
		t.Fatal("expected cached payload reuse")
	}
}

func TestResolveImageAttachmentsMissingFile(t *testing.T) {
	t.Parallel()
	ag := newVisionTestAgent(t)
	msgs := ag.resolveImageAttachments([]ollama.Message{{
		Role:    "user",
		Content: "look: " + vision.MarkerFor(filepath.Join(t.TempDir(), "gone.png")),
	}})
	if len(msgs[0].Images) != 0 {
		t.Fatalf("expected no images, got %d", len(msgs[0].Images))
	}
	if !strings.Contains(msgs[0].Content, "(image unavailable:") {
		t.Fatalf("expected unavailable note, got %q", msgs[0].Content)
	}
}

func TestResolveImageAttachmentsRespectsCap(t *testing.T) {
	t.Parallel()
	ag := newVisionTestAgent(t)
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < vision.MaxImagesPerMessage+2; i++ {
		p := filepath.Join(dir, strings.Repeat("f", i+1)+".png")
		writeTestPNG(t, p)
		b.WriteString(vision.MarkerFor(p))
		b.WriteString("\n")
	}
	msgs := ag.resolveImageAttachments([]ollama.Message{{Role: "user", Content: b.String()}})
	if len(msgs[0].Images) != vision.MaxImagesPerMessage {
		t.Fatalf("expected %d images, got %d", vision.MaxImagesPerMessage, len(msgs[0].Images))
	}
	if !strings.Contains(msgs[0].Content, "image omitted") {
		t.Fatalf("expected omission note, got %q", msgs[0].Content)
	}
}

func TestResolveImageAttachmentsIgnoresAssistantEcho(t *testing.T) {
	t.Parallel()
	ag := newVisionTestAgent(t)
	marker := vision.MarkerFor("/tmp/echo.png")
	msgs := ag.resolveImageAttachments([]ollama.Message{{Role: "assistant", Content: "I saw " + marker}})
	if len(msgs[0].Images) != 0 || !strings.Contains(msgs[0].Content, marker) {
		t.Fatalf("assistant echo must pass through untouched: %+v", msgs[0])
	}
}

func TestImageMarkerCountsTowardSessionTokens(t *testing.T) {
	t.Parallel()
	ag := newVisionTestAgent(t)
	before := ag.sess.TokenEstimate()
	ag.sess.AddUser(vision.MarkerFor("/tmp/a.png"))
	if got := ag.sess.TokenEstimate() - before; got < vision.EstimatedTokensPerImage {
		t.Fatalf("expected image token cost, delta=%d", got)
	}
}

func TestSystemPromptAdvertisesVision(t *testing.T) {
	t.Parallel()
	ag := newVisionTestAgent(t)
	got := ag.buildSystemPrompt()
	if !strings.Contains(got, "Vision: AVAILABLE") {
		t.Fatalf("expected vision line, got:\n%s", got)
	}

	ag.cfg.VisionAvailable = false
	ag.sysPrompt = promptCache{}
	if got := ag.buildSystemPrompt(); !strings.Contains(got, "Vision: NOT available") {
		t.Fatalf("expected no-vision line, got:\n%s", got)
	}

	ag.cfg.VisionKnown = false
	ag.sysPrompt = promptCache{}
	if got := ag.buildSystemPrompt(); !strings.Contains(got, "Vision: UNKNOWN") {
		t.Fatalf("expected unknown line, got:\n%s", got)
	}
}

func TestBuildOllamaMessagesReservesImageBudget(t *testing.T) {
	t.Parallel()
	ag := newVisionTestAgent(t)
	ag.cfg.ContextWindow = 4000 // budget floor stays at 12000 chars
	// 7000 text chars alone would fit the 12000 budget; with the 6000-char
	// image proxy it overflows, so the old turn must be trimmed.
	ag.sess.AddUser(strings.Repeat("x", 7000) + "\n" + vision.MarkerFor("/tmp/old.png"))
	ag.sess.AddUser("new question")
	msgs := ag.buildOllamaMessages("SYSTEM")
	if len(msgs) != 2 || msgs[1].Content != "new question" {
		t.Fatalf("expected trimming to drop the image turn, got %d messages", len(msgs))
	}
}
