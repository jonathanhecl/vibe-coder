package config

import (
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// RecommendedModel returns the model auto-detected for this machine's RAM. It
// is the same value Load falls back to when no main model is configured, so the
// first-run wizard can offer the model the runtime would pick anyway.
func RecommendedModel() string {
	return autoDetectModel()
}

func autoDetectModel() string {
	ramGB := detectRAMGB()
	switch {
	case ramGB >= 48:
		return "qwen3.5:27b"
	case ramGB >= 24:
		return "qwen3.5:9b"
	case ramGB >= 12:
		return "qwen3.5:4b"
	default:
		return "qwen3.5:2b"
	}
}

func detectRAMGB() int {
	if v := strings.TrimSpace(os.Getenv("VIBE_CODER_RAM_GB")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			return parsed
		}
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		_ = bi
	}
	// Conservative fallback for portability; users can override with VIBE_CODER_RAM_GB.
	return 8
}
