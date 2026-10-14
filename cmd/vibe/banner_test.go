package main

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// bannerRowPlain returns the plain value of the row with the given label.
func bannerRowPlain(t *testing.T, rows []tui.RoleRow, label string) string {
	t.Helper()
	for _, row := range rows {
		if row.Label == label {
			return row.Plain
		}
	}
	t.Fatalf("missing %s row: %#v", label, rows)
	return ""
}

func TestBannerRowsRoleWiring(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:        "llama3.2:3b",
		SidecarModel: "qwen3.5:4b",
		OllamaHost:   "http://localhost:11434",
	}
	rows := bannerRows(cfg, "session-123", false)

	want := []struct {
		label string
		plain string
	}{
		{"Session", "session-123"},
		{"Model", "[ollama] llama3.2:3b"},
		{"Sidecar", "[ollama] qwen3.5:4b"},
		{"Decision", "Fallback to [ollama] qwen3.5:4b  (set it with /setmodel decision MODEL)"},
		{"Embedding", "text-only retrieval (no encoder)  (set it with /setmodel embedding MODEL)"},
		{"Vision", "images go to main, then sidecar  (set it with /setmodel vision MODEL)"},
		{"Rabbit", "Fallback to [ollama] llama3.2:3b  (set it with /setmodel rabbit MODEL)"},
	}
	if len(rows) != len(want) {
		t.Fatalf("expected %d rows, got %d: %#v", len(want), len(rows), rows)
	}
	for i, expected := range want {
		if rows[i].Label != expected.label {
			t.Fatalf("row %d label = %q, want %q", i, rows[i].Label, expected.label)
		}
		if rows[i].Plain != expected.plain {
			t.Errorf("%s = %q, want %q", expected.label, rows[i].Plain, expected.plain)
		}
	}
}

func TestBannerRowsSessionState(t *testing.T) {
	t.Parallel()

	base := func() *config.Config {
		return &config.Config{Model: "llama3.2:3b", OllamaHost: "http://localhost:11434"}
	}

	cfg := base()
	if got := bannerRowPlain(t, bannerRows(cfg, "session-abc", true), "Session"); got != "session-abc (resumed)" {
		t.Fatalf("resumed session = %q", got)
	}

	cfg = base()
	cfg.Temporal = true
	if got := bannerRowPlain(t, bannerRows(cfg, "session-tmp", false), "Session"); got != "session-tmp (temporal)" {
		t.Fatalf("temporal session = %q", got)
	}

	cfg = base()
	cfg.Isolated = true
	if got := bannerRowPlain(t, bannerRows(cfg, "session-iso", false), "Session"); got != "session-iso (isolated)" {
		t.Fatalf("isolated session = %q", got)
	}
}

func TestBannerRowsDecisionBound(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:         "llama3.2:3b",
		SidecarModel:  "qwen3.5:4b",
		DecisionModel: "decision-v1",
		OllamaHost:    "http://localhost:11434",
	}
	rows := bannerRows(cfg, "session-123", false)
	if got := bannerRowPlain(t, rows, "Decision"); got != "[ollama] decision-v1 (run '/yes assisted' to enable)" {
		t.Fatalf("decision row = %q", got)
	}

	cfg.AssistedYes = true
	rows = bannerRows(cfg, "session-123", false)
	if got := bannerRowPlain(t, rows, "Decision"); got != "[ollama] decision-v1 (assisted enabled)" {
		t.Fatalf("assisted decision row = %q", got)
	}
}

func TestBannerRowsSidecarDisabled(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:           "llama3.2:3b",
		SidecarModel:    "qwen3.5:4b",
		SidecarDisabled: true,
		OllamaHost:      "http://localhost:11434",
	}
	rows := bannerRows(cfg, "session-123", false)
	if got := bannerRowPlain(t, rows, "Sidecar"); got != "(disabled)" {
		t.Fatalf("disabled sidecar row = %q", got)
	}

	cfg = &config.Config{
		Model:              "llama3.2:3b",
		SidecarModel:       "qwen3.5:4b",
		SidecarSkipSession: true,
		OllamaHost:         "http://localhost:11434",
	}
	rows = bannerRows(cfg, "session-123", false)
	if got := bannerRowPlain(t, rows, "Sidecar"); got != "[ollama] qwen3.5:4b (off this session)" {
		t.Fatalf("skipped sidecar row = %q", got)
	}
}

func TestBannerRowsDecisionEndpoint(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:            "llama3.2:3b",
		DecisionEndpoint: "http://mac-mini.local:8765",
		OllamaHost:       "http://localhost:11434",
	}
	rows := bannerRows(cfg, "session-123", false)
	if got := bannerRowPlain(t, rows, "Decision"); !strings.Contains(got, "http://mac-mini.local:8765") {
		t.Fatalf("expected the decision endpoint in the banner row, got %q", got)
	}

	styled := startupBanner(cfg, "session-123", false, tui.NewStyleForTest(true))
	if !strings.Contains(styled, "http://mac-mini.local:8765") {
		t.Fatalf("expected the decision endpoint in the styled banner, got:\n%s", styled)
	}
	plain := startupBanner(cfg, "session-123", false, tui.Style{})
	if !strings.Contains(plain, "http://mac-mini.local:8765") {
		t.Fatalf("expected the decision endpoint in the plain banner, got:\n%s", plain)
	}
}

func TestBannerRowsNilConfig(t *testing.T) {
	t.Parallel()

	rows := bannerRows(nil, "session-nil", false)
	if len(rows) != len(config.AllRoles)+1 {
		t.Fatalf("expected %d rows, got %d", len(config.AllRoles)+1, len(rows))
	}
	if got := bannerRowPlain(t, rows, "Session"); got != "session-nil" {
		t.Fatalf("session row = %q", got)
	}
	if got := bannerRowPlain(t, rows, "Model"); got != "(set it with /setmodel main MODEL)" {
		t.Fatalf("model row for nil config = %q", got)
	}
}

func TestBannerRowsOffline(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:      "llama3.2:3b",
		OllamaHost: "http://localhost:11434",
		Offline:    true,
	}
	rows := bannerRows(cfg, "session-off", false)
	if got := bannerRowPlain(t, rows, "Offline"); got != "enabled (network blocked)" {
		t.Fatalf("offline row = %q", got)
	}

	plain := startupBanner(cfg, "session-off", false, tui.Style{})
	if !strings.Contains(plain, "Offline: enabled (network blocked)") {
		t.Fatalf("expected offline in plain banner, got:\n%s", plain)
	}
	styled := startupBanner(cfg, "session-off", false, tui.NewStyleForTest(true))
	if !strings.Contains(styled, "enabled (network blocked)") {
		t.Fatalf("expected offline in styled banner, got:\n%s", styled)
	}
}

func TestBannerRowsVisionTag(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:         "llama3.2:3b",
		SidecarModel:  "moondream",
		OllamaHost:    "http://localhost:11434",
		VisionByModel: map[string]bool{"moondream": true},
	}
	rows := bannerRows(cfg, "session-123", false)
	if got := bannerRowPlain(t, rows, "Sidecar"); got != "[ollama] moondream  vision" {
		t.Fatalf("sidecar row = %q", got)
	}
	if got := bannerRowPlain(t, rows, "Model"); got != "[ollama] llama3.2:3b" {
		t.Fatalf("model row must not carry the tag, got %q", got)
	}
	// Fallback targets describe where a role goes, not what it runs, so they
	// stay untagged even when the fallback model can see.
	if got := bannerRowPlain(t, rows, "Rabbit"); strings.Contains(got, "vision") {
		t.Fatalf("fallback row must not carry the vision tag, got %q", got)
	}

	styled := startupBanner(cfg, "session-123", false, tui.NewStyleForTest(true))
	if !strings.Contains(styled, "\x1b[38;5;45mvision\x1b[0m") {
		t.Fatalf("expected the cyan vision tag in the styled banner, got:\n%s", styled)
	}
}

func TestBannerWordmark(t *testing.T) {

	t.Parallel()

	lines := strings.Split(bannerWordmark, "\n")
	if len(lines) != 6 {
		t.Fatalf("expected 6 wordmark lines, got %d", len(lines))
	}
	if strings.Contains(bannerWordmark, "\x1b") {
		t.Fatal("wordmark must not contain ANSI escape codes")
	}
}

func TestStartupBannerDecisionAssisted(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:         "llama3.2:3b",
		DecisionModel: "decision-2b:latest",
		AssistedYes:   true,
		OllamaHost:    "http://localhost:11434",
	}

	// Styled banner: (assisted enabled) in green (\x1b[32m)
	styled := startupBanner(cfg, "session-123", false, tui.NewStyleForTest(true))
	if !strings.Contains(styled, "\x1b[32m(assisted enabled)\x1b[0m") {
		t.Fatalf("expected green '(assisted enabled)' in styled banner, got:\n%s", styled)
	}
	if !strings.Contains(styled, "decision-2b:latest") {
		t.Fatalf("expected decision model in styled banner, got:\n%s", styled)
	}

	// Plain/unstyled banner
	plain := startupBanner(cfg, "session-123", false, tui.Style{})
	if !strings.Contains(plain, "(assisted enabled)") {
		t.Fatalf("expected '(assisted enabled)' in plain banner, got:\n%s", plain)
	}
}

func TestStartupBannerDecisionWithoutAssisted(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:         "llama3.2:3b",
		DecisionModel: "decision-2b:latest",
		AssistedYes:   false,
		OllamaHost:    "http://localhost:11434",
	}

	styled := startupBanner(cfg, "session-123", false, tui.NewStyleForTest(true))
	if strings.Contains(styled, "(assisted enabled)") {
		t.Fatalf("did not expect '(assisted enabled)' in styled banner when AssistedYes is false, got:\n%s", styled)
	}
	if !strings.Contains(styled, "\x1b[97m(run '/yes assisted' to enable)\x1b[0m") {
		t.Fatalf("expected white '(run '/yes assisted' to enable)' in styled banner, got:\n%s", styled)
	}

	plain := startupBanner(cfg, "session-123", false, tui.Style{})
	if strings.Contains(plain, "(assisted enabled)") {
		t.Fatalf("did not expect '(assisted enabled)' in plain banner when AssistedYes is false, got:\n%s", plain)
	}
	if !strings.Contains(plain, "(run '/yes assisted' to enable)") {
		t.Fatalf("expected '(run '/yes assisted' to enable)' in plain banner, got:\n%s", plain)
	}
}

func TestStartupBannerStylesRoleValues(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:        "llama3.2:3b",
		SidecarModel: "qwen3.5:4b",
		OllamaHost:   "http://localhost:11434",
	}
	styled := startupBanner(cfg, "session-123", false, tui.NewStyleForTest(true))

	// Provider stays gray while the model stays bright white.
	if !strings.Contains(styled, "\x1b[90m[ollama]\x1b[0m \x1b[97mllama3.2:3b\x1b[0m") {
		t.Fatalf("expected a gray provider and white model in the styled banner, got:\n%s", styled)
	}
	// The unset-role hint is highlighted in yellow.
	if !strings.Contains(styled, "\x1b[33m(set it with /setmodel rabbit MODEL)\x1b[0m") {
		t.Fatalf("expected a yellow hint for the rabbit role, got:\n%s", styled)
	}
}
