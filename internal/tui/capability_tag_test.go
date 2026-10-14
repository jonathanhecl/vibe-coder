package tui

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestCapabilityTagUsesStablePalette(t *testing.T) {
	t.Parallel()

	st := NewStyleForTest(true)
	cases := map[string]int{
		config.CapabilityCompletion: 250,
		config.CapabilityTools:      178,
		config.CapabilityThinking:   141,
		config.CapabilityVision:     45,
		config.CapabilityEmbedding:  205,
		config.CapabilityDecision:   208,
	}
	for tag, code := range cases {
		want := "\x1b[38;5;" + itoa(code) + "m" + tag + "\x1b[0m"
		if got := st.CapabilityTag(tag); got != want {
			t.Fatalf("CapabilityTag(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestCapabilityTagIsCaseInsensitiveAndFallsBackToGray(t *testing.T) {
	t.Parallel()

	st := NewStyleForTest(true)
	if got := st.CapabilityTag("VISION"); !strings.Contains(got, "38;5;45") {
		t.Fatalf("expected VISION to reuse the vision color, got %q", got)
	}
	if got := st.CapabilityTag("recommended"); got != "\x1b[90mrecommended\x1b[0m" {
		t.Fatalf("expected unknown tag in gray, got %q", got)
	}
}

func TestCapabilityTagIsPlainWhenColorsDisabled(t *testing.T) {
	t.Parallel()

	st := NewStyleForTest(false)
	if got := st.CapabilityTag(config.CapabilityTools); got != config.CapabilityTools {
		t.Fatalf("expected plain tag when colors are disabled, got %q", got)
	}
}

func TestLimeUsesPalette154(t *testing.T) {
	t.Parallel()

	if got := NewStyleForTest(true).Lime("VIBE"); got != "\x1b[38;5;154mVIBE\x1b[0m" {
		t.Fatalf("unexpected lime output: %q", got)
	}
	if got := NewStyleForTest(false).Lime("VIBE"); got != "VIBE" {
		t.Fatalf("expected plain lime output when colors are disabled, got %q", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
