package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLSPDefaults(t *testing.T) {
	t.Parallel()
	cfg := defaultConfig(t.TempDir(), t.TempDir(), t.TempDir())
	if cfg.LSP.Enabled != LSPAuto {
		t.Fatalf("Enabled default = %q, want %q", cfg.LSP.Enabled, LSPAuto)
	}
	if !cfg.LSP.AfterEdit {
		t.Fatal("AfterEdit should default to true")
	}
	if cfg.LSP.TimeoutMS != defaultLSPTimeout {
		t.Fatalf("TimeoutMS default = %d, want %d", cfg.LSP.TimeoutMS, defaultLSPTimeout)
	}
	if len(cfg.LSP.Servers) != 0 {
		t.Fatalf("Servers default = %v, want none", cfg.LSP.Servers)
	}
	if !cfg.LSP.Requested() || cfg.LSP.Demanded() {
		t.Fatal("auto should be requested but not demanded")
	}
}

func TestLSPFromConfigFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "vibe-coder.env")
	content := "LSP_ENABLED=on\n" +
		"LSP_SERVERS=go:gopls -remote=auto\n" +
		"LSP_AFTER_EDIT=false\n" +
		"LSP_TIMEOUT_MS=1234\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig(dir, dir, dir)
	if err := applyConfigFile(cfg, path); err != nil {
		t.Fatalf("applyConfigFile: %v", err)
	}
	cfg.LSP.normalize()

	if cfg.LSP.Enabled != LSPOn {
		t.Fatalf("Enabled = %q, want %q", cfg.LSP.Enabled, LSPOn)
	}
	if cfg.LSP.AfterEdit {
		t.Fatal("expected AfterEdit=false from the file")
	}
	if cfg.LSP.TimeoutMS != 1234 {
		t.Fatalf("TimeoutMS = %d, want 1234", cfg.LSP.TimeoutMS)
	}
	want := map[string][]string{"go": {"gopls", "-remote=auto"}}
	if !reflect.DeepEqual(cfg.LSP.Servers, want) {
		t.Fatalf("Servers = %v, want %v", cfg.LSP.Servers, want)
	}
}

func TestLSPEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vibe-coder.env")
	if err := os.WriteFile(path, []byte("LSP_ENABLED=off\nLSP_TIMEOUT_MS=7000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIBE_CODER_LSP_ENABLED", "on")
	t.Setenv("VIBE_CODER_LSP_AFTER_EDIT", "no")
	t.Setenv("VIBE_CODER_LSP_SERVERS", "python:pylsp")

	cfg := defaultConfig(dir, dir, dir)
	if err := applyConfigFile(cfg, path); err != nil {
		t.Fatalf("applyConfigFile: %v", err)
	}
	applyEnv(cfg)
	cfg.LSP.normalize()

	if cfg.LSP.Enabled != LSPOn {
		t.Fatalf("env should override file: Enabled = %q", cfg.LSP.Enabled)
	}
	if cfg.LSP.AfterEdit {
		t.Fatal("expected AfterEdit=false from env")
	}
	if cfg.LSP.TimeoutMS != 7000 {
		t.Fatalf("file value should stand when env unset: TimeoutMS = %d", cfg.LSP.TimeoutMS)
	}
	if want := map[string][]string{"python": {"pylsp"}}; !reflect.DeepEqual(cfg.LSP.Servers, want) {
		t.Fatalf("Servers = %v, want %v", cfg.LSP.Servers, want)
	}
}

// TestLSPFlagOverridesEnvAndFile walks the whole precedence chain through Load,
// because the CLI is the only layer whose form is not a string value: it forces
// the switch rather than spelling it.
func TestLSPFlagOverridesEnvAndFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vibe-coder.env")
	if err := os.WriteFile(path, []byte("LSP_ENABLED=on\nLSP_TIMEOUT_MS=7000\nLSP_AFTER_EDIT=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIBE_CODER_CONFIG", path)
	t.Setenv("VIBE_CODER_LSP_ENABLED", "on")

	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LSP.Enabled != LSPOn || cfg.LSP.TimeoutMS != 7000 {
		t.Fatalf("file/env baseline = %q/%d", cfg.LSP.Enabled, cfg.LSP.TimeoutMS)
	}

	cfg, err = Load([]string{"--no-lsp"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LSP.Enabled != LSPOff {
		t.Fatalf("--no-lsp should win over env and file, got %q", cfg.LSP.Enabled)
	}
	if cfg.LSP.Requested() {
		t.Fatal("an off switch should not be requested")
	}

	cfg, err = Load([]string{"--lsp", "--lsp-timeout", "1500", "--no-lsp-after-edit", "--lsp-servers", "go:gopls -mod=readonly"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LSP.Enabled != LSPOn || !cfg.LSP.Demanded() {
		t.Fatalf("--lsp should demand the feature, got %q", cfg.LSP.Enabled)
	}
	if cfg.LSP.TimeoutMS != 1500 {
		t.Fatalf("--lsp-timeout should win, got %d", cfg.LSP.TimeoutMS)
	}
	if cfg.LSP.AfterEdit {
		t.Fatal("--no-lsp-after-edit should win over the file")
	}
	if want := map[string][]string{"go": {"gopls", "-mod=readonly"}}; !reflect.DeepEqual(cfg.LSP.Servers, want) {
		t.Fatalf("Servers = %v, want %v", cfg.LSP.Servers, want)
	}
}

func TestLSPFlagConflict(t *testing.T) {
	t.Parallel()
	_, err := parseCLI([]string{"--lsp", "--no-lsp"})
	if err == nil || !strings.Contains(err.Error(), "cannot use both --lsp and --no-lsp") {
		t.Fatalf("err = %v, want a conflict naming both flags", err)
	}
}

func TestLSPTimeoutClamping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   int
		want int
	}{
		{name: "zero takes the default", in: 0, want: defaultLSPTimeout},
		{name: "negative takes the default", in: -20, want: defaultLSPTimeout},
		{name: "below the floor clamps up", in: 100, want: minLSPTimeout},
		{name: "inside the range is kept", in: 2500, want: 2500},
		{name: "above the ceiling clamps down", in: 99999, want: maxLSPTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lsp := LSPConfig{TimeoutMS: tc.in}
			lsp.normalize()
			if lsp.TimeoutMS != tc.want {
				t.Fatalf("TimeoutMS = %d, want %d", lsp.TimeoutMS, tc.want)
			}
		})
	}
}

func TestNormalizeLSPEnabled(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{in: "", want: LSPAuto},
		{in: "auto", want: LSPAuto},
		{in: "AUTO", want: LSPAuto},
		{in: "default", want: LSPAuto},
		{in: "empty", want: LSPAuto},
		{in: "on", want: LSPOn},
		{in: "TRUE", want: LSPOn},
		{in: "yes", want: LSPOn},
		{in: "1", want: LSPOn},
		{in: "enabled", want: LSPOn},
		{in: " off ", want: LSPOff},
		{in: "false", want: LSPOff},
		{in: "no", want: LSPOff},
		{in: "0", want: LSPOff},
		{in: "disabled", want: LSPOff},
		{in: "none", want: LSPOff},
		// A typo must not silently turn the feature off: the safe answer is the
		// one that only checks when a server actually answers.
		{in: "maybe", want: LSPAuto},
		{in: "of", want: LSPAuto},
	}
	for _, tc := range cases {
		if got := normalizeLSPEnabled(tc.in); got != tc.want {
			t.Errorf("normalizeLSPEnabled(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseLSPServers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want map[string][]string
	}{
		{name: "empty", in: "", want: nil},
		{name: "blank entries", in: " , ,", want: nil},
		{name: "one family", in: "go:gopls", want: map[string][]string{"go": {"gopls"}}},
		{name: "family is folded", in: " GO : gopls -mode=auto ", want: map[string][]string{"go": {"gopls", "-mode=auto"}}},
		{name: "several families", in: "go:gopls,python:pylsp", want: map[string][]string{"go": {"gopls"}, "python": {"pylsp"}}},
		{name: "no colon is skipped", in: "gopls", want: nil},
		{name: "empty family is skipped", in: ":gopls", want: nil},
		{name: "empty command is skipped", in: "go:", want: nil},
		{name: "the first colon wins", in: "go:C:/tools/gopls.exe", want: map[string][]string{"go": {"C:/tools/gopls.exe"}}},
		{name: "a good entry survives a bad one", in: "broken,go:gopls", want: map[string][]string{"go": {"gopls"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLSPServers(tc.in)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseLSPServers(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLSPTimeoutDuration(t *testing.T) {
	t.Parallel()
	lsp := LSPConfig{TimeoutMS: 2500}
	if got := lsp.Timeout(); got != 2500*time.Millisecond {
		t.Fatalf("Timeout = %s, want 2.5s", got)
	}
	unset := LSPConfig{}
	if got := unset.Timeout(); got != 0 {
		t.Fatalf("an unnormalized config must not invent a timeout, got %s", got)
	}
}
