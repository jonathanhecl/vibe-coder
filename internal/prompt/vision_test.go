package prompt

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestBuildVisionBorrowedAndSecondOpinion(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Cwd: t.TempDir(), Model: "text-model", SidecarModel: "moondream",
		VisionKnown: true, VisionAvailable: false,
		SidecarVisionKnown: true, SidecarVisionAvailable: true,
	}
	got := Build(cfg)
	if !strings.Contains(got, "Vision: via SIDECAR") || !strings.Contains(got, "DescribeImage") {
		t.Fatalf("expected borrowed-vision guidance, got:\n%s", got)
	}

	cfg.VisionAvailable = true
	got = Build(cfg)
	if !strings.Contains(got, "Vision: AVAILABLE") || !strings.Contains(got, "second opinion") {
		t.Fatalf("expected second-opinion guidance, got:\n%s", got)
	}
}

func TestBuildVisionRoleTakesPrecedence(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Cwd: t.TempDir(), Model: "text-model", SidecarModel: "moondream",
		VisionKnown: true, VisionAvailable: false,
		SidecarVisionKnown: true, SidecarVisionAvailable: true,
		RoleModels: map[config.Role]config.RoleModel{
			config.RoleVision: {Provider: config.DefaultProviderName, Model: "llava:latest"},
		},
	}
	got := Build(cfg)
	if !strings.Contains(got, "Vision: via VISION MODEL") || !strings.Contains(got, "llava:latest") {
		t.Fatalf("expected vision-role guidance, got:\n%s", got)
	}
	if strings.Contains(got, "Vision: via SIDECAR") {
		t.Fatalf("the vision role must win over the sidecar route, got:\n%s", got)
	}

	// With a seeing main model the vision role is offered for a second opinion.
	cfg.VisionAvailable = true
	got = Build(cfg)
	if !strings.Contains(got, "Vision: AVAILABLE") || !strings.Contains(got, "vision role") {
		t.Fatalf("expected the vision role named for second opinions, got:\n%s", got)
	}
}

// Without a bound vision role the derived routes stay exactly as before.
func TestBuildVisionWithoutRoleKeepsDerivedRoutes(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Cwd: t.TempDir(), Model: "text-model", SidecarModel: "moondream",
		VisionKnown: true, VisionAvailable: false,
		SidecarVisionKnown: true, SidecarVisionAvailable: true,
	}
	got := Build(cfg)
	if !strings.Contains(got, "Vision: via SIDECAR") || strings.Contains(got, "vision role") {
		t.Fatalf("expected the sidecar route and no vision role, got:\n%s", got)
	}
}

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
			if tc.available && !strings.Contains(got, "do NOT call Read or DescribeImage on them") {
				t.Fatalf("expected attached-image guidance, got:\n%s", got)
			}
			if !tc.available && tc.known && !strings.Contains(got, "/setmodel") {
				t.Fatalf("expected model-switch suggestion, got:\n%s", got)
			}
		})
	}
}

// When some model can see, the prompt advertises Screenshot for visual
// self-checks; when nobody can see it stays silent about it.
func TestBuildVisionAdvertisesScreenshot(t *testing.T) {
	t.Parallel()
	seeing := &config.Config{
		Cwd: t.TempDir(), Model: "text-model", SidecarModel: "moondream",
		VisionKnown: true, VisionAvailable: false,
		SidecarVisionKnown: true, SidecarVisionAvailable: true,
	}
	if got := Build(seeing); !strings.Contains(got, "call Screenshot with a focused question") {
		t.Fatalf("expected Screenshot guidance, got:\n%s", got)
	}
	blind := &config.Config{
		Cwd: t.TempDir(), Model: "text-model",
		VisionKnown: true, VisionAvailable: false,
	}
	if got := Build(blind); strings.Contains(got, "call Screenshot") {
		t.Fatalf("expected no Screenshot guidance without a vision route, got:\n%s", got)
	}
}
