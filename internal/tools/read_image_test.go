package tools

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

func writeTestImage(t *testing.T, path string) {
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

func TestReadImageReturnsMarker(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "photo.png")
	writeTestImage(t, path)

	res := NewReadTool().Execute(context.Background(), map[string]any{"file_path": path})
	if res.IsError {
		t.Fatalf("expected marker, got error: %s", res.Output)
	}
	if res.Output != vision.MarkerFor(path) {
		t.Fatalf("expected marker %q, got %q", vision.MarkerFor(path), res.Output)
	}
	if !strings.Contains(res.HintsForModel, "attached") {
		t.Fatalf("expected attach hint, got %q", res.HintsForModel)
	}
}

func TestReadImageRejectsUnsupportedFormat(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "photo.webp")
	if err := os.WriteFile(path, []byte("RIFF....WEBP"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := NewReadTool().Execute(context.Background(), map[string]any{"file_path": path})
	if !res.IsError {
		t.Fatalf("expected format error, got %q", res.Output)
	}
	if !strings.Contains(res.Output, "unsupported image format") {
		t.Fatalf("expected clear format error, got %q", res.Output)
	}
}

func TestReadImageRejectsOversize(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "big.png")
	writeTestImage(t, path)
	if err := os.Truncate(path, vision.MaxInputBytes+1); err != nil {
		t.Skipf("truncate not supported: %v", err)
	}
	res := NewReadTool().Execute(context.Background(), map[string]any{"file_path": path})
	if !res.IsError || !strings.Contains(res.Output, "too large") {
		t.Fatalf("expected size error, got %+v", res)
	}
}
