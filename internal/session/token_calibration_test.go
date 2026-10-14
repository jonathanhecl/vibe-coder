package session

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestTokenCalibratorIgnoresInvalidPairs(t *testing.T) {
	t.Parallel()

	c := &TokenCalibrator{}
	c.Observe(0, 100)
	c.Observe(100, 0)
	c.Observe(-5, 100)
	if c.Samples() != 0 {
		t.Fatalf("expected no samples, got %d", c.Samples())
	}
	if got := c.Factor(); got != 1 {
		t.Fatalf("expected factor 1, got %v", got)
	}
}

func TestTokenCalibratorLearnsRatio(t *testing.T) {
	t.Parallel()

	c := &TokenCalibrator{}
	c.Observe(100, 200)
	if got := c.Factor(); got != 2.0 {
		t.Fatalf("expected factor 2, got %v", got)
	}
	if got := c.Adjust(100); got != 200 {
		t.Fatalf("expected 200, got %d", got)
	}
}

func TestTokenCalibratorSmoothsAndClamps(t *testing.T) {
	t.Parallel()

	c := &TokenCalibrator{}
	c.Observe(100, 200) // 2.0
	c.Observe(100, 100) // 2.0*0.8 + 1.0*0.2 = 1.8
	if got := c.Factor(); got < 1.79 || got > 1.81 {
		t.Fatalf("expected ~1.8, got %v", got)
	}
	c.Observe(100, 100000) // observed is clamped to 2.0
	if got := c.Factor(); got > calibrationMaxRatio {
		t.Fatalf("ratio must stay clamped, got %v", got)
	}
}

func TestNilCalibratorIsInert(t *testing.T) {
	t.Parallel()

	var c *TokenCalibrator
	c.Observe(100, 200)
	if got := c.Factor(); got != 1 {
		t.Fatalf("nil factor must be 1, got %v", got)
	}
	if got := c.Adjust(42); got != 42 {
		t.Fatalf("nil adjust must pass through, got %d", got)
	}
	if got := c.Samples(); got != 0 {
		t.Fatalf("nil samples must be 0, got %d", got)
	}
}

func TestSessionCalibratesTokenEstimate(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir(), ContextWindow: 4096}
	s := New(cfg)
	s.AddUser("hello world")

	raw := s.TokenEstimate()
	if raw <= 0 {
		t.Fatalf("expected a positive raw estimate, got %d", raw)
	}
	s.ObservePromptUsage(raw, raw*2)
	if got := s.TokenEstimate(); got != raw*2 {
		t.Fatalf("expected the estimate doubled to %d, got %d", raw*2, got)
	}
}

func TestCompactUsesCalibratedEstimate(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir(), ContextWindow: 4096}
	s := New(cfg)
	s.SetClient(sidecarClient{})
	for i := 0; i < 40; i++ {
		s.AddUser(strings.Repeat("x", 160))
	}
	raw := s.TokenEstimate()
	threshold := int(0.7 * float64(cfg.ContextWindow))
	if raw >= threshold || raw*2 <= threshold {
		t.Skipf("estimate %d does not straddle the threshold %d", raw, threshold)
	}
	if err := s.Compact(context.Background(), false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := s.MessageCount(); got != 40 {
		t.Fatalf("expected no compaction below the threshold, got %d messages", got)
	}
	s.ObservePromptUsage(raw, raw*2)
	if err := s.Compact(context.Background(), false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := s.MessageCount(); got >= 40 {
		t.Fatalf("expected compaction once calibrated above the threshold, got %d messages", got)
	}
}
