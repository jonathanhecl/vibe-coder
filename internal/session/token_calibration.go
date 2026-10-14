package session

import "sync"

// Token calibration bounds. The estimator counts bytes and CJK runes, which is
// cheap but coarse; the ratio between the estimate and the provider's real
// prompt tokens is learned from observed usage so budgets and compaction
// thresholds follow reality instead of a fixed guess.
const (
	calibrationMinRatio  = 0.5
	calibrationMaxRatio  = 2.0
	calibrationSmoothing = 0.2
)

// TokenCalibrator learns the ratio between estimated and real prompt tokens. It
// is safe for concurrent use and nil-safe: a nil calibrator corrects nothing.
type TokenCalibrator struct {
	mu      sync.Mutex
	ratio   float64
	samples int
}

// Observe records one (estimated, actual) pair. Pairs where either side is not
// positive are ignored, and the observed ratio is clamped and smoothed so a
// single odd measurement cannot swing the correction.
func (c *TokenCalibrator) Observe(estimated, actual int) {
	if c == nil || estimated <= 0 || actual <= 0 {
		return
	}
	observed := float64(actual) / float64(estimated)
	if observed < calibrationMinRatio {
		observed = calibrationMinRatio
	}
	if observed > calibrationMaxRatio {
		observed = calibrationMaxRatio
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.samples == 0 {
		c.ratio = observed
	} else {
		c.ratio = c.ratio*(1-calibrationSmoothing) + observed*calibrationSmoothing
	}
	c.samples++
}

// Factor returns the learned correction, and 1 when nothing was observed yet.
func (c *TokenCalibrator) Factor() float64 {
	if c == nil {
		return 1
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.samples == 0 || c.ratio <= 0 {
		return 1
	}
	return c.ratio
}

// Adjust scales a raw estimate by the learned factor, keeping at least one so a
// corrected estimate never collapses to zero.
func (c *TokenCalibrator) Adjust(estimate int) int {
	if c == nil || estimate <= 0 {
		return estimate
	}
	adjusted := int(float64(estimate) * c.Factor())
	if adjusted < 1 {
		return 1
	}
	return adjusted
}

// Samples reports how many observations were folded in.
func (c *TokenCalibrator) Samples() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.samples
}
