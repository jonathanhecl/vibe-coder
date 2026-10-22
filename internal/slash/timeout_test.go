package slash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetTimeoutSetAndStatus(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)

	if _, _, err := Dispatch(ctx, "/settimeout"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Permission prompt timeout: 3m (default)") {
		t.Fatalf("expected default status, got %q", out.String())
	}

	cases := []struct {
		arg  string
		want time.Duration
	}{
		{"3m", 3 * time.Minute},
		{"5m", 5 * time.Minute},
		{"10m", 10 * time.Minute},
		{"30s", 30 * time.Second},
		{"120", 120 * time.Second},
		{"90s", 90 * time.Second},
	}
	for _, tc := range cases {
		out.Reset()
		if _, _, err := Dispatch(ctx, "/settimeout "+tc.arg); err != nil {
			t.Fatal(err)
		}
		if ctx.Cfg.PermissionTimeout != tc.want {
			t.Fatalf("%s: expected %s, got %s", tc.arg, tc.want, ctx.Cfg.PermissionTimeout)
		}
		if !strings.Contains(out.String(), "Run /save to persist") {
			t.Fatalf("%s: expected persist hint, got %q", tc.arg, out.String())
		}
	}

	out.Reset()
	if _, _, err := Dispatch(ctx, "/settimeout off"); err != nil {
		t.Fatal(err)
	}
	if ctx.Cfg.PermissionTimeout >= 0 {
		t.Fatalf("off must disable the countdown, got %s", ctx.Cfg.PermissionTimeout)
	}
	if ctx.Cfg.EffectivePermissionTimeout() != 0 {
		t.Fatalf("effective timeout must be 0 when disabled, got %s", ctx.Cfg.EffectivePermissionTimeout())
	}

	out.Reset()
	if _, _, err := Dispatch(ctx, "/settimeout status"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "off (waits until answered)") {
		t.Fatalf("expected disabled status, got %q", out.String())
	}
}

func TestSetTimeoutRejectsInvalidValue(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	ctx.Cfg.PermissionTimeout = 90 * time.Second

	for _, arg := range []string{"soon", "3 minutes", "-5m"} {
		out.Reset()
		if _, _, err := Dispatch(ctx, "/settimeout "+arg); err != nil {
			t.Fatal(err)
		}
		if ctx.Cfg.PermissionTimeout != 90*time.Second {
			t.Fatalf("%q must keep the previous value, got %s", arg, ctx.Cfg.PermissionTimeout)
		}
		if !strings.Contains(out.String(), "Invalid permission timeout") {
			t.Fatalf("%q: expected error message, got %q", arg, out.String())
		}
	}
}

func TestSetTimeoutPersistsViaSave(t *testing.T) {
	ctx, _, _ := newContextTestCtx(t)
	ctx.Cfg.ConfigFile = filepath.Join(t.TempDir(), "vibe-coder.env")

	if _, _, err := Dispatch(ctx, "/settimeout 90s"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Dispatch(ctx, "/save"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(ctx.Cfg.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "PERMISSION_TIMEOUT=90s") {
		t.Fatalf("expected the setting in the config file, got %q", string(data))
	}
}
