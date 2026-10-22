package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEffectivePermissionTimeout(t *testing.T) {
	t.Parallel()
	if d := (*Config)(nil).EffectivePermissionTimeout(); d != 3*time.Minute {
		t.Fatalf("nil config: got %v want 3m", d)
	}
	if d := (&Config{}).EffectivePermissionTimeout(); d != 3*time.Minute {
		t.Fatalf("unset: got %v want 3m", d)
	}
	if d := (&Config{PermissionTimeout: 45 * time.Second}).EffectivePermissionTimeout(); d != 45*time.Second {
		t.Fatalf("custom: got %v want 45s", d)
	}
	if d := (&Config{PermissionTimeout: -1}).EffectivePermissionTimeout(); d != 0 {
		t.Fatalf("disabled: got %v want 0", d)
	}
}

func TestParsePermissionTimeout(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"3m", 3 * time.Minute, true},
		{" 90s ", 90 * time.Second, true},
		{"30", 30 * time.Second, true},
		{"120", 120 * time.Second, true},
		{"0", -1, true},
		{"off", -1, true},
		{"never", -1, true},
		{"-5m", -1, true},
		{"", 0, false},
		{"three minutes", 0, false},
	}
	for _, tc := range cases {
		got, ok := ParsePermissionTimeout(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParsePermissionTimeout(%q) = (%v, %v), want (%v, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestLoadPermissionTimeoutFromFileEnvAndCLI(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localapp"))
	t.Setenv("APPDATA", filepath.Join(tmp, "appdata"))
	t.Setenv("HOME", tmp)

	cfgPath := filepath.Join(tmp, "vibe-coder.env")
	if err := os.WriteFile(cfgPath, []byte("PERMISSION_TIMEOUT=90s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIBE_CODER_CONFIG", cfgPath)
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EffectivePermissionTimeout() != 90*time.Second {
		t.Fatalf("file: got %v want 90s", cfg.EffectivePermissionTimeout())
	}

	t.Setenv("VIBE_CODER_PERMISSION_TIMEOUT", "2m")
	cfg, err = Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EffectivePermissionTimeout() != 2*time.Minute {
		t.Fatalf("env over file: got %v want 2m", cfg.EffectivePermissionTimeout())
	}

	cfg, err = Load([]string{"--permission-timeout", "30s"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EffectivePermissionTimeout() != 30*time.Second {
		t.Fatalf("cli over env: got %v want 30s", cfg.EffectivePermissionTimeout())
	}
}

func TestLoadPermissionTimeoutOffDisablesCountdown(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localapp"))
	t.Setenv("VIBE_CODER_PERMISSION_TIMEOUT", "off")
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if d := cfg.EffectivePermissionTimeout(); d != 0 {
		t.Fatalf("off: got %v want 0", d)
	}
}

func TestLoadInvalidPermissionTimeoutFlag(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localapp"))
	_, err := Load([]string{"--permission-timeout", "soon"})
	if err == nil {
		t.Fatal("expected an error for an unparsable duration")
	}
	if !strings.Contains(err.Error(), "permission-timeout") && !strings.Contains(err.Error(), "invalid value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSavePermissionTimeoutRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localapp"))
	t.Setenv("HOME", tmp)
	cfgPath := filepath.Join(tmp, "vibe-coder.env")

	cases := []struct {
		in       time.Duration
		wantLine string
	}{
		{90 * time.Second, "PERMISSION_TIMEOUT=90s"},
		{3 * time.Minute, "PERMISSION_TIMEOUT=3m"},
		{-1, "PERMISSION_TIMEOUT=off"},
	}
	for _, tc := range cases {
		cfg := &Config{ConfigFile: cfgPath, PermissionTimeout: tc.in}
		if err := SaveModelSettings(cfg); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), tc.wantLine) {
			t.Fatalf("%s: expected %q in %q", tc.in, tc.wantLine, string(data))
		}
		t.Setenv("VIBE_CODER_CONFIG", cfgPath)
		loaded, err := Load(nil)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.PermissionTimeout != tc.in {
			t.Fatalf("%s: reloaded as %s", tc.in, loaded.PermissionTimeout)
		}
	}
}

func TestSaveUnsetPermissionTimeoutWritesNoKey(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localapp"))
	t.Setenv("HOME", tmp)
	cfgPath := filepath.Join(tmp, "vibe-coder.env")

	cfg := &Config{ConfigFile: cfgPath}
	if err := SaveModelSettings(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "PERMISSION_TIMEOUT") {
		t.Fatalf("unset timeout must not be written, got %q", string(data))
	}
}
