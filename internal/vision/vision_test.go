package vision

import (
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
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

func TestMarkerRoundTrip(t *testing.T) {
	t.Parallel()
	marker := MarkerFor("/tmp/photo.jpg")
	if !strings.Contains(marker, "/tmp/photo.jpg") {
		t.Fatalf("marker should embed path, got %q", marker)
	}
	paths := ParseMarkers("see this:\n" + marker + "\nand " + MarkerFor("/a/b.png"))
	if len(paths) != 2 || paths[0] != "/tmp/photo.jpg" || paths[1] != "/a/b.png" {
		t.Fatalf("unexpected parsed paths: %v", paths)
	}
	if CountMarkers("no markers") != 0 {
		t.Fatal("expected zero markers")
	}
}

func TestImageExtensionDetection(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"a.jpg", "a.JPEG", "a.png", "a.gif", "a.bmp"} {
		if !IsImagePath(p) || !IsSupportedImage(p) {
			t.Fatalf("expected %s to be a supported image", p)
		}
	}
	for _, p := range []string{"a.webp", "a.svg", "a.heic"} {
		if !IsImagePath(p) {
			t.Fatalf("expected %s to be image-like", p)
		}
		if IsSupportedImage(p) {
			t.Fatalf("expected %s to be unsupported", p)
		}
	}
	for _, p := range []string{"a.md", "a.txt", "a.go", ""} {
		if IsImagePath(p) {
			t.Fatalf("expected %s to not be an image", p)
		}
	}
}

func TestEncodeFileDownscalesAndReturnsJPEG(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "big.png")
	writePNG(t, path, 2000, 1200)

	payload, err := EncodeFile(path)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	img, format, err := image.Decode(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("payload does not decode: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("expected jpeg payload, got %s", format)
	}
	bounds := img.Bounds()
	if bounds.Dx() > MaxDimension || bounds.Dy() > MaxDimension {
		t.Fatalf("expected downscale to %d, got %dx%d", MaxDimension, bounds.Dx(), bounds.Dy())
	}
}

func TestEncodeFileRejectsBadInputs(t *testing.T) {
	tmp := t.TempDir()
	if _, err := EncodeFile(filepath.Join(tmp, "missing.png")); err == nil {
		t.Fatal("expected error for missing file")
	}
	text := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(text, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeFile(text); err == nil {
		t.Fatal("expected error for non-image bytes")
	}
	dir := filepath.Join(tmp, "asdir.png")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeFile(dir); err == nil {
		t.Fatal("expected error for directory input")
	}
	big := filepath.Join(tmp, "big.png")
	writePNG(t, big, 10, 10)
	if err := os.Truncate(big, MaxInputBytes+1); err != nil {
		t.Skipf("truncate not supported: %v", err)
	}
	if _, err := EncodeFile(big); err == nil {
		t.Fatal("expected size cap error")
	}
}

func TestCacheEncodesOnceAndEvicts(t *testing.T) {
	calls := 0
	c := NewCache()
	for i := 0; i < 3; i++ {
		if _, err := c.GetOrEncode("k", func() (string, error) {
			calls++
			return "payload", nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("expected single encode, got %d", calls)
	}
	for i := 0; i < ImageCacheEntries+5; i++ {
		key := strings.Repeat("k", i+1)
		if _, err := c.GetOrEncode(key, func() (string, error) { return "p", nil }); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.Size(); got != ImageCacheEntries {
		t.Fatalf("expected cache cap %d, got %d", ImageCacheEntries, got)
	}
}

func TestCacheKeyChangesOnWrite(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "a.png")
	writePNG(t, path, 8, 8)
	first, err := CacheKey(path)
	if err != nil {
		t.Fatal(err)
	}
	writePNG(t, path, 16, 16)
	second, err := CacheKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("expected cache key to change after rewrite")
	}
}
