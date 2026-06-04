package permissions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestLoadMergesLegacyPermissionsJSON(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "vibe-coder.env")
	if err := os.WriteFile(cfgPath, []byte("MODEL=test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(tmp, "permissions.json")
	if err := os.WriteFile(legacy, []byte(`{"write":"allow","edit":"deny"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(&config.Config{PermFile: cfgPath})
	if !m.Check("Write", map[string]any{}, nil) {
		t.Fatal("expected write allowed from legacy file")
	}
	if m.Check("Edit", map[string]any{}, nil) {
		t.Fatal("expected edit denied")
	}
}
