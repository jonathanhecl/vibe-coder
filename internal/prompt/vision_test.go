package prompt

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestBuildVisionSectionStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		available bool
		known     bool
		want      string
	}{
		{"available", true, true, "Vision: AVAILABLE"},
		{"unavailable", false, true, "Vision: NOT available"},
		{"unknown", false, false, "Vision: UNKNOWN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &config.Config{
				Cwd:             t.TempDir(),
				Model:           "test-model",
				VisionAvailable: tc.available,
				VisionKnown:     tc.known,
			}
			got := Build(cfg)
			if !strings.Contains(got, "# Vision") || !strings.Contains(got, tc.want) {
				t.Fatalf("expected vision section %q, got:\n%s", tc.want, got)
			}
			if tc.available && !strings.Contains(got, "use Read on the image file") {
				t.Fatalf("expected attach guidance, got:\n%s", got)
			}
			if !tc.available && tc.known && !strings.Contains(got, "/model") {
				t.Fatalf("expected model-switch suggestion, got:\n%s", got)
			}
		})
	}
}
