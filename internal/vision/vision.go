package vision

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	// MaxInputBytes caps the raw image file accepted by Read.
	MaxInputBytes = 15 * 1024 * 1024
	// MaxDimension bounds the longest side after downscaling.
	MaxDimension = 1024
	// JPEGQuality balances payload size against classification accuracy.
	JPEGQuality = 80
	// MaxImagesPerMessage caps attachments resolved on a single message.
	MaxImagesPerMessage = 5
	// EstimatedTokensPerImage is the planning constant used for compaction
	// triggers and transcript budgeting. Vision tokens are not text tokens,
	// so this is a conservative proxy, not a measurement.
	EstimatedTokensPerImage = 1500
	// EstimatedCharsPerImage mirrors the token estimate for char-based
	// transcript budgets (4 chars per token).
	EstimatedCharsPerImage = EstimatedTokensPerImage * 4
	// ImageCacheEntries bounds the encoded-image cache per session.
	ImageCacheEntries = 20
)

var supportedExtensions = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".bmp":  true,
}

// imageLikeExtensions are recognised as images but cannot be decoded by
// this build. They produce a clear error instead of a marker.
var imageLikeExtensions = map[string]bool{
	".webp": true,
	".tiff": true,
	".tif":  true,
	".svg":  true,
	".ico":  true,
	".avif": true,
	".heic": true,
	".heif": true,
}

// IsImagePath reports whether path looks like an image file (decodable or not).
func IsImagePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	return supportedExtensions[ext] || imageLikeExtensions[ext]
}

// IsSupportedImage reports whether path has a decodable image extension.
func IsSupportedImage(path string) bool {
	return supportedExtensions[strings.ToLower(filepath.Ext(strings.TrimSpace(path)))]
}

// SupportedFormats returns a human-readable list for prompts and errors.
func SupportedFormats() string {
	return "JPEG, PNG, GIF, BMP"
}

// markerRe matches markers produced by MarkerFor. The path runs to the
// closing bracket; absolute paths never contain "]" in practice.
var markerRe = regexp.MustCompile(`\[image path=([^\]]+)\]`)

// MarkerFor builds the transcript marker for an absolute image path. The
// marker is plain text: it survives session persistence and compaction,
// and resolves to real image bytes at send time.
func MarkerFor(absPath string) string {
	return fmt.Sprintf("[image path=%s]", absPath)
}

// ParseMarkers returns the image paths referenced by markers in content,
// in order of appearance.
func ParseMarkers(content string) []string {
	matches := markerRe.FindAllStringSubmatch(content, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if p := strings.TrimSpace(m[1]); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// CountMarkers returns the number of image markers in content.
func CountMarkers(content string) int {
	return len(markerRe.FindAllString(content, -1))
}

// Cache memoizes encoded images per session so each file is downscaled and
// base64-encoded once no matter how many turns reference it.
type Cache struct {
	mu      sync.Mutex
	entries map[string]string
	order   []string
}

// NewCache returns an empty image cache.
func NewCache() *Cache {
	return &Cache{entries: map[string]string{}}
}

// GetOrEncode returns the cached base64 payload for key, or encodes it via
// encode and caches the result. Failures are not cached.
func (c *Cache) GetOrEncode(key string, encode func() (string, error)) (string, error) {
	if c == nil {
		return encode()
	}
	c.mu.Lock()
	if payload, ok := c.entries[key]; ok {
		c.mu.Unlock()
		return payload, nil
	}
	c.mu.Unlock()
	payload, err := encode()
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; !ok {
		c.entries[key] = payload
		c.order = append(c.order, key)
		for len(c.order) > ImageCacheEntries {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
	}
	return payload, nil
}

// Size returns the number of cached entries.
func (c *Cache) Size() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Get returns a cached payload without encoding on miss.
func (c *Cache) Get(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	payload, ok := c.entries[key]
	return payload, ok
}

// Put stores a payload, evicting the oldest entries past the cap.
func (c *Cache) Put(key, payload string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; !ok {
		c.entries[key] = payload
		c.order = append(c.order, key)
		for len(c.order) > ImageCacheEntries {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
	}
}
