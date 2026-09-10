package tools

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/sidecar"
)

type visionFakeClient struct {
	calls int32
	reply string
	err   error
	last  ollama.ChatRequest
}

func (f *visionFakeClient) Chat(ctx context.Context, req ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	return nil, errors.New("not used")
}
func (f *visionFakeClient) ChatSync(ctx context.Context, req ollama.ChatRequest) (ollama.ChatResponse, error) {
	atomic.AddInt32(&f.calls, 1)
	f.last = req
	if f.err != nil {
		return ollama.ChatResponse{}, f.err
	}
	return ollama.ChatResponse{Content: f.reply}, nil
}
func (f *visionFakeClient) Tags(ctx context.Context) ([]ollama.Model, error) {
	return nil, errors.New("not used")
}
func (f *visionFakeClient) Version(ctx context.Context) (string, error) {
	return "", errors.New("not used")
}
func (f *visionFakeClient) Pull(ctx context.Context, model string, p func(ollama.PullEvent)) error {
	return errors.New("not used")
}

func writeDescribeToolImage(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 16), uint8(y * 16), 100, 255})
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

func TestDescribeImageUsesSidecar(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "photo.png")
	writeDescribeToolImage(t, path)
	cfg := &config.Config{
		Model: "text-model", SidecarModel: "moondream",
		VisionKnown: true, VisionAvailable: false,
		SidecarVisionKnown: true, SidecarVisionAvailable: true,
	}
	fc := &visionFakeClient{reply: "A cat."}
	tool := NewDescribeImageTool(cfg, fc, sidecar.New(cfg, fc))

	res := tool.Execute(context.Background(), map[string]any{"file_path": path, "question": "What animal?"})
	if res.IsError {
		t.Fatalf("expected answer, got error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "A cat.") || !strings.Contains(res.Output, "moondream") {
		t.Fatalf("expected attributed answer, got %q", res.Output)
	}
	if fc.last.Model != "moondream" || len(fc.last.Messages[1].Images) != 1 {
		t.Fatalf("expected sidecar request with image, got %+v", fc.last)
	}
}

func TestDescribeImageFallsBackToMain(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "photo.png")
	writeDescribeToolImage(t, path)
	cfg := &config.Config{
		Model: "llava", VisionKnown: true, VisionAvailable: true,
	}
	fc := &visionFakeClient{reply: "A dog."}
	// No sidecar pool: main model looks itself.
	tool := NewDescribeImageTool(cfg, fc, sidecar.New(cfg, fc))

	res := tool.Execute(context.Background(), map[string]any{"file_path": path})
	if res.IsError {
		t.Fatalf("expected answer, got error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "A dog.") || !strings.Contains(res.Output, "llava") {
		t.Fatalf("expected main-model answer, got %q", res.Output)
	}
}

func TestDescribeImageFailsHonestly(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "photo.png")
	writeDescribeToolImage(t, path)
	cfg := &config.Config{
		Model: "text-model", SidecarModel: "other-text",
		VisionKnown: true, VisionAvailable: false,
		SidecarVisionKnown: true, SidecarVisionAvailable: false,
	}
	fc := &visionFakeClient{reply: "x"}
	tool := NewDescribeImageTool(cfg, fc, sidecar.New(cfg, fc))

	res := tool.Execute(context.Background(), map[string]any{"file_path": path})
	if !res.IsError || !strings.Contains(res.Output, "neither") {
		t.Fatalf("expected honest failure, got %+v", res)
	}
	if atomic.LoadInt32(&fc.calls) != 0 {
		t.Fatal("expected no network calls")
	}

	// Bad inputs fail before any call.
	for _, params := range []map[string]any{
		{"file_path": ""},
		{"file_path": filepath.Join(t.TempDir(), "missing.png")},
	} {
		if res := tool.Execute(context.Background(), params); !res.IsError {
			t.Fatalf("expected error for %v", params)
		}
	}
}
