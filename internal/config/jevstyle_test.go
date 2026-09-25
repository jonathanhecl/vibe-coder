package config

import "testing"

func TestJevstyleInUse(t *testing.T) {
	t.Parallel()

	var cfg Config
	if cfg.JevstyleInUse() {
		t.Fatal("expected JevstyleInUse false when empty")
	}

	cfg.JevstyleModel = "jev-v1"
	if !cfg.JevstyleInUse() {
		t.Fatal("expected JevstyleInUse true when model set")
	}

	cfg.JevstyleModel = "   "
	if cfg.JevstyleInUse() {
		t.Fatal("expected JevstyleInUse false when whitespace")
	}

	var nilCfg *Config
	if nilCfg.JevstyleInUse() {
		t.Fatal("expected JevstyleInUse false for nil config")
	}
}
