package vision

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"

	// Register decoders. BMP comes from x/image; WebP is intentionally
	// unsupported (no decoder in this family) and rejected with a clear error.
	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// EncodeFile loads an image from disk, downscales it so the longest side is
// at most MaxDimension, and returns a base64 JPEG payload ready for the
// Ollama /api/chat images field.
func EncodeFile(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("image path is empty")
	}
	info, err := os.Lstat(trimmed)
	if err != nil {
		return "", fmt.Errorf("read image %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refusing symlink image: %s", trimmed)
	}
	if info.IsDir() {
		return "", fmt.Errorf("image path is a directory: %s", trimmed)
	}
	if info.Size() > MaxInputBytes {
		return "", fmt.Errorf("image too large: %s (%d bytes, max %d)", trimmed, info.Size(), MaxInputBytes)
	}
	data, err := os.ReadFile(trimmed)
	if err != nil {
		return "", fmt.Errorf("read image %q: %w", path, err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("decode image %s: %w (supported formats: %s)", trimmed, err, SupportedFormats())
	}
	img = downscale(img)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return "", fmt.Errorf("encode image %s: %w", trimmed, err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// CacheKey identifies a file revision for the encode cache.
func CacheKey(path string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("resolve image path: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", fmt.Errorf("stat image: %w", err)
	}
	return fmt.Sprintf("%s|%d|%d", abs, info.Size(), info.ModTime().UnixNano()), nil
}

func downscale(img image.Image) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	longest := w
	if h > longest {
		longest = h
	}
	if longest <= MaxDimension {
		return img
	}
	scale := float64(MaxDimension) / float64(longest)
	dst := image.NewRGBA(image.Rect(0, 0, int(float64(w)*scale), int(float64(h)*scale)))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
	return dst
}
