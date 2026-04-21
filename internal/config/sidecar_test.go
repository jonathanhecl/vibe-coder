package config

import "testing"

func TestSidecarInUse(t *testing.T) {
	t.Parallel()
	var cfg Config
	cfg.SidecarModel = "m"
	if !cfg.SidecarInUse() {
		t.Fatal("expected in use with model only")
	}
	cfg.SidecarDisabled = true
	if cfg.SidecarInUse() {
		t.Fatal("config disable should win")
	}
	cfg.SidecarDisabled = false
	cfg.SidecarSkipSession = true
	if cfg.SidecarInUse() {
		t.Fatal("session skip should disable")
	}
	cfg.SidecarSkipSession = false
	cfg.SidecarModel = ""
	if cfg.SidecarInUse() {
		t.Fatal("empty model should disable")
	}
}

func TestPersistSidecarOffFromSave(t *testing.T) {
	t.Parallel()
	c := &Config{SidecarSkipSession: true}
	c.PersistSidecarOffFromSave(true)
	if !c.SidecarDisabled {
		t.Fatal("expected SidecarDisabled after /save + --no-sidecar")
	}
	c2 := &Config{SidecarSkipSession: true}
	c2.PersistSidecarOffFromSave(false)
	if c2.SidecarDisabled {
		t.Fatal("without /save must not set SidecarDisabled")
	}
	c3 := &Config{SidecarSkipSession: false}
	c3.PersistSidecarOffFromSave(true)
	if c3.SidecarDisabled {
		t.Fatal("without --no-sidecar must not set SidecarDisabled")
	}
}
