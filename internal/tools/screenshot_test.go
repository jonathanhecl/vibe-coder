package tools

import (
	"context"
	"errors"
	"image"
	"image/color"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/screenshot"
	"github.com/jonathanhecl/vibe-coder/internal/sidecar"
)

func fakeScreenshotCapture(t *testing.T, img image.Image, err error) func(context.Context, screenshot.Request) (image.Image, error) {
	t.Helper()
	if img == nil {
		rgba := image.NewRGBA(image.Rect(0, 0, 16, 16))
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				rgba.Set(x, y, color.RGBA{uint8(x * 16), uint8(y * 16), 100, 255})
			}
		}
		img = rgba
	}
	return func(context.Context, screenshot.Request) (image.Image, error) {
		return img, err
	}
}

func screenshotVisionConfig() *config.Config {
	return &config.Config{
		Model: "text-model", SidecarModel: "moondream",
		VisionKnown: true, VisionAvailable: false,
		SidecarVisionKnown: true, SidecarVisionAvailable: true,
	}
}

func TestScreenshotAnswersViaSidecar(t *testing.T) {
	t.Parallel()
	cfg := screenshotVisionConfig()
	fc := &visionFakeClient{reply: "Yes, the Save button is visible."}
	tool := NewScreenshotTool(cfg, fc, sidecar.New(cfg, fc))
	tool.capture = fakeScreenshotCapture(t, nil, nil)

	res := tool.Execute(context.Background(), map[string]any{
		"target":   "window",
		"question": "Is the Save button visible?",
	})
	if res.IsError {
		t.Fatalf("expected answer, got error: %s", res.Output)
	}
	for _, want := range []string{"Yes, the Save button is visible.", "moondream", "active window", "16x16"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("expected %q in output, got %q", want, res.Output)
		}
	}
	// The image must reach the model as one inline image, and the base64
	// payload must never appear in the tool result.
	if len(fc.last.Messages[1].Images) != 1 {
		t.Fatalf("expected 1 image in the request, got %+v", fc.last.Messages[1].Images)
	}
	if strings.Contains(res.Output, fc.last.Messages[1].Images[0]) {
		t.Fatal("the image payload must not leak into the result")
	}
	if fc.last.Messages[0].Content != sidecar.FocusedVisualSystem {
		t.Fatalf("expected the focused system prompt, got %q", fc.last.Messages[0].Content)
	}
	if !strings.Contains(fc.last.Messages[1].Content, "screenshot (active window)") {
		t.Fatalf("expected the screenshot label, got %q", fc.last.Messages[1].Content)
	}
}

func TestScreenshotDefaultQuestionUsesDescriptiveSystem(t *testing.T) {
	t.Parallel()
	cfg := screenshotVisionConfig()
	fc := &visionFakeClient{reply: "A desktop."}
	tool := NewScreenshotTool(cfg, fc, sidecar.New(cfg, fc))
	tool.capture = fakeScreenshotCapture(t, nil, nil)

	res := tool.Execute(context.Background(), map[string]any{})
	if res.IsError {
		t.Fatalf("expected answer, got error: %s", res.Output)
	}
	if fc.last.Messages[0].Content != sidecar.VisualAssistantSystem {
		t.Fatalf("expected descriptive system prompt, got %q", fc.last.Messages[0].Content)
	}
	if !strings.Contains(res.Output, "all monitors") {
		t.Fatalf("expected the default all-monitors label, got %q", res.Output)
	}
}

func TestScreenshotRejectsBadParams(t *testing.T) {
	t.Parallel()
	cfg := screenshotVisionConfig()
	fc := &visionFakeClient{reply: "x"}
	tool := NewScreenshotTool(cfg, fc, sidecar.New(cfg, fc))
	tool.capture = fakeScreenshotCapture(t, nil, nil)

	for _, params := range []map[string]any{
		{"target": "desktop"},
		{"monitor": "0"},
		{"monitor": "abc"},
		{"target": "window", "monitor": "1"},
	} {
		if res := tool.Execute(context.Background(), params); !res.IsError {
			t.Fatalf("expected error for %v", params)
		}
	}
	if atomic.LoadInt32(&fc.calls) != 0 {
		t.Fatal("invalid params must not reach the vision model")
	}
}

func TestScreenshotCaptureErrorSurfaced(t *testing.T) {
	t.Parallel()
	cfg := screenshotVisionConfig()
	fc := &visionFakeClient{reply: "x"}
	tool := NewScreenshotTool(cfg, fc, sidecar.New(cfg, fc))
	tool.capture = fakeScreenshotCapture(t, nil, errors.New("no backend"))

	res := tool.Execute(context.Background(), map[string]any{})
	if !res.IsError || !strings.Contains(res.Output, "screenshot failed") || !strings.Contains(res.Output, "no backend") {
		t.Fatalf("expected the capture error, got %+v", res)
	}
	if atomic.LoadInt32(&fc.calls) != 0 {
		t.Fatal("a failed capture must not reach the vision model")
	}
}

func TestScreenshotNoVisionRouteFailsHonestly(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Model: "text-model", SidecarModel: "other-text",
		VisionKnown: true, VisionAvailable: false,
		SidecarVisionKnown: true, SidecarVisionAvailable: false,
	}
	fc := &visionFakeClient{reply: "x"}
	tool := NewScreenshotTool(cfg, fc, sidecar.New(cfg, fc))
	tool.capture = fakeScreenshotCapture(t, nil, nil)

	res := tool.Execute(context.Background(), map[string]any{})
	if !res.IsError || !strings.Contains(res.Output, "neither") || !strings.Contains(res.Output, "/setmodel vision") {
		t.Fatalf("expected honest failure naming /setmodel vision, got %+v", res)
	}
	if atomic.LoadInt32(&fc.calls) != 0 {
		t.Fatal("expected no network calls")
	}
}

// JSON numbers arrive as float64; a numeric monitor index must be accepted.
func TestScreenshotAcceptsNumericMonitor(t *testing.T) {
	t.Parallel()
	cfg := screenshotVisionConfig()
	fc := &visionFakeClient{reply: "A monitor."}
	tool := NewScreenshotTool(cfg, fc, sidecar.New(cfg, fc))
	tool.capture = fakeScreenshotCapture(t, nil, nil)

	res := tool.Execute(context.Background(), map[string]any{"monitor": float64(2)})
	if res.IsError {
		t.Fatalf("expected answer, got error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "monitor 2") {
		t.Fatalf("expected the monitor 2 label, got %q", res.Output)
	}
}
