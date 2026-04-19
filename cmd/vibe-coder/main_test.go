package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionFlagSmoke(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("go", "run", "./cmd/vibe-coder", "--version")
	cmd.Dir = filepath.Clean("../..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run --version failed: %v\noutput: %s", err, string(out))
	}

	got := strings.TrimSpace(string(out))
	if got != "vibe-coder dev" {
		t.Fatalf("unexpected version output: %q", got)
	}
}

