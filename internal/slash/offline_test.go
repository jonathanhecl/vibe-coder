package slash

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func TestOfflineCommandToggle(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{Offline: false, Cwd: tmp, SessionsDir: tmp}
	s := session.New(cfg)
	perm := permissions.NewManager(cfg)
	out := &bytes.Buffer{}

	ctx := &Ctx{
		Cfg:     cfg,
		Session: s,
		Perm:    perm,
		Out:     out,
	}

	// 1. Toggle ON
	handled, shouldExit, err := Dispatch(ctx, "/offline")
	if err != nil || !handled || shouldExit {
		t.Fatalf("expected handled=true, shouldExit=false, err=nil; got handled=%v exit=%v err=%v", handled, shouldExit, err)
	}
	if !cfg.Offline {
		t.Fatal("expected cfg.Offline to be true after toggle")
	}
	if !perm.OfflineMode() {
		t.Fatal("expected perm.OfflineMode to be true after toggle")
	}
	if !strings.Contains(out.String(), "Offline mode enabled") {
		t.Fatalf("expected 'Offline mode enabled' in output, got: %s", out.String())
	}

	// 2. Status check
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/offline status")
	if err != nil || !handled || shouldExit {
		t.Fatalf("expected handled=true, shouldExit=false, err=nil; got handled=%v exit=%v err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "Offline mode is currently ON") {
		t.Fatalf("expected 'Offline mode is currently ON' in output, got: %s", out.String())
	}

	// 3. Toggle OFF
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/offline")
	if err != nil || !handled || shouldExit {
		t.Fatalf("expected handled=true, shouldExit=false, err=nil; got handled=%v exit=%v err=%v", handled, shouldExit, err)
	}
	if cfg.Offline {
		t.Fatal("expected cfg.Offline to be false after toggle")
	}
	if perm.OfflineMode() {
		t.Fatal("expected perm.OfflineMode to be false after toggle")
	}
	if !strings.Contains(out.String(), "Offline mode disabled") {
		t.Fatalf("expected 'Offline mode disabled' in output, got: %s", out.String())
	}

	// 4. Explicit ON and OFF
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/offline on")
	if err != nil || !handled || shouldExit {
		t.Fatalf("dispatch error: %v", err)
	}
	if !cfg.Offline || !perm.OfflineMode() {
		t.Fatal("expected offline mode ON after '/offline on'")
	}

	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/offline off")
	if err != nil || !handled || shouldExit {
		t.Fatalf("dispatch error: %v", err)
	}
	if cfg.Offline || perm.OfflineMode() {
		t.Fatal("expected offline mode OFF after '/offline off'")
	}
}

func TestStatusIncludesOfflineRow(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{Offline: false, Cwd: tmp, SessionsDir: tmp}
	s := session.New(cfg)
	perm := permissions.NewManager(cfg)
	out := &bytes.Buffer{}

	ctx := &Ctx{
		Cfg:     cfg,
		Session: s,
		Perm:    perm,
		Agent:   &fakePlanAgent{},
		Out:     out,
	}

	// Status when offline is disabled
	writeStatus(ctx, tui.NewStyleForTest(false))
	if !strings.Contains(out.String(), "Offline: disabled") {
		t.Fatalf("expected 'Offline: disabled' in output, got: %s", out.String())
	}

	// Status when offline is enabled
	cfg.Offline = true
	perm.SetOfflineMode(true)
	out.Reset()
	writeStatus(ctx, tui.NewStyleForTest(false))
	if !strings.Contains(out.String(), "Offline: enabled (network blocked)") {
		t.Fatalf("expected 'Offline: enabled (network blocked)' in output, got: %s", out.String())
	}
}
