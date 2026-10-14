package sidecar

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func writeDescribeTestImage(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 24, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 24; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 10), uint8(y * 10), 50, 255})
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

func newDescribeTestPool(reply string) (*Pool, *fakeClient) {
	cfg := &config.Config{SidecarModel: "moondream"}
	fc := &fakeClient{reply: reply}
	return New(cfg, fc), fc
}

func TestDescribeImageSendsPixelsAndCaches(t *testing.T) {
	t.Parallel()
	photo := filepath.Join(t.TempDir(), "photo.png")
	writeDescribeTestImage(t, photo)

	pool, fc := newDescribeTestPool("A red bicycle.")
	desc, err := pool.DescribeImage(context.Background(), photo, "What vehicle is this?")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if desc != "A red bicycle." {
		t.Fatalf("unexpected description: %q", desc)
	}
	if len(fc.lastReqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(fc.lastReqs))
	}
	req := fc.lastReqs[0]
	if req.Model != "moondream" {
		t.Fatalf("expected sidecar model, got %q", req.Model)
	}
	if len(req.Messages) != 2 || len(req.Messages[1].Images) != 1 || req.Messages[1].Images[0] == "" {
		t.Fatalf("expected user message with one image, got %+v", req.Messages)
	}
	if !strings.Contains(req.Messages[1].Content, "What vehicle is this?") {
		t.Fatalf("expected custom question, got %q", req.Messages[1].Content)
	}
	// Thinking must be explicitly disabled on the describe call: on a thinking
	// sidecar the reasoning would consume the small NumPredict budget and
	// return an empty description.
	if req.Think == nil || req.Think.IsActive() {
		t.Fatalf("expected thinking disabled for the sidecar describe call, got %+v", req.Think)
	}

	// Second call serves from cache: no new request.
	if _, err := pool.DescribeImage(context.Background(), photo, "What vehicle is this?"); err != nil {
		t.Fatal(err)
	}
	if len(fc.lastReqs) != 1 {
		t.Fatalf("expected cache hit, got %d requests", len(fc.lastReqs))
	}
}

func TestDescribeImageDefaultsQuestion(t *testing.T) {
	t.Parallel()
	photo := filepath.Join(t.TempDir(), "photo.png")
	writeDescribeTestImage(t, photo)
	pool, fc := newDescribeTestPool("Something.")
	if _, err := pool.DescribeImage(context.Background(), photo, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fc.lastReqs[0].Messages[1].Content, "Describe this image") {
		t.Fatalf("expected default question, got %q", fc.lastReqs[0].Messages[1].Content)
	}
}

func TestDescribeImageRejectsBadInputs(t *testing.T) {
	t.Parallel()
	pool, _ := newDescribeTestPool("x")
	if _, err := pool.DescribeImage(context.Background(), "", "q"); err == nil {
		t.Fatal("expected empty path error")
	}
	webp := filepath.Join(t.TempDir(), "a.webp")
	if err := os.WriteFile(webp, []byte("RIFF"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.DescribeImage(context.Background(), webp, "q"); err == nil {
		t.Fatal("expected format error")
	}
	if _, err := pool.DescribeImage(context.Background(), filepath.Join(t.TempDir(), "gone.png"), "q"); err == nil {
		t.Fatal("expected missing file error")
	}
}

func TestDescribeImageRequiresEnabledPool(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{} // no sidecar model
	pool := New(cfg, &fakeClient{reply: "x"})
	photo := filepath.Join(t.TempDir(), "photo.png")
	writeDescribeTestImage(t, photo)
	if _, err := pool.DescribeImage(context.Background(), photo, "q"); err == nil {
		t.Fatal("expected disabled-pool error")
	}
}

func TestVisualSystemFor(t *testing.T) {
	t.Parallel()
	if got := VisualSystemFor(""); got != VisualAssistantSystem {
		t.Fatal("blank question should use the descriptive system")
	}
	if got := VisualSystemFor(DefaultDescribeQuestion); got != VisualAssistantSystem {
		t.Fatal("default question should use the descriptive system")
	}
	if got := VisualSystemFor("Is the Save button visible?"); got != FocusedVisualSystem {
		t.Fatal("custom question should use the focused system")
	}
}

func TestDescribePayloadSendsImageAndFocusedSystem(t *testing.T) {
	t.Parallel()
	pool, fc := newDescribeTestPool("Yes, it is visible.")
	desc, err := pool.DescribePayload(context.Background(), "aGVsbG8=", "screenshot (all monitors)", "Is the Save button visible?")
	if err != nil {
		t.Fatalf("describe payload: %v", err)
	}
	if desc != "Yes, it is visible." {
		t.Fatalf("unexpected answer: %q", desc)
	}
	if len(fc.lastReqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(fc.lastReqs))
	}
	req := fc.lastReqs[0]
	if req.Messages[0].Content != FocusedVisualSystem {
		t.Fatalf("expected focused system prompt, got %q", req.Messages[0].Content)
	}
	if len(req.Messages[1].Images) != 1 || req.Messages[1].Images[0] != "aGVsbG8=" {
		t.Fatalf("expected the payload image, got %+v", req.Messages[1].Images)
	}
	if !strings.Contains(req.Messages[1].Content, "screenshot (all monitors)") {
		t.Fatalf("expected the label in the user message, got %q", req.Messages[1].Content)
	}

	// Payloads are unique, so nothing is cached: a second call with the
	// same question still hits the model.
	if _, err := pool.DescribePayload(context.Background(), "aGVsbG8=", "screenshot (all monitors)", "Is the Save button visible?"); err != nil {
		t.Fatal(err)
	}
	if len(fc.lastReqs) != 2 {
		t.Fatalf("expected no caching for payloads, got %d requests", len(fc.lastReqs))
	}
}

func TestDescribePayloadDefaultsQuestion(t *testing.T) {
	t.Parallel()
	pool, fc := newDescribeTestPool("A screen.")
	if _, err := pool.DescribePayload(context.Background(), "aGVsbG8=", "screenshot (all monitors)", ""); err != nil {
		t.Fatal(err)
	}
	if fc.lastReqs[0].Messages[0].Content != VisualAssistantSystem {
		t.Fatalf("expected descriptive system for the default question, got %q", fc.lastReqs[0].Messages[0].Content)
	}
}

func TestDescribePayloadRejectsBadInputs(t *testing.T) {
	t.Parallel()
	pool, _ := newDescribeTestPool("x")
	if _, err := pool.DescribePayload(context.Background(), "", "label", "q"); err == nil {
		t.Fatal("expected empty payload error")
	}
	disabled := New(&config.Config{}, &fakeClient{reply: "x"})
	if _, err := disabled.DescribePayload(context.Background(), "aA==", "label", "q"); err == nil {
		t.Fatal("expected disabled-pool error")
	}
}
