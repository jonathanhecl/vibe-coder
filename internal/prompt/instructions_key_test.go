package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstructionsDiskKeyChangesWithMtime(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(p, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	k1 := InstructionsDiskKey(dir)
	if err := os.WriteFile(p, []byte("v2-longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	k2 := InstructionsDiskKey(dir)
	if k1 == k2 {
		t.Fatal("expected disk key to change after file edit")
	}
	if !strings.Contains(k1, "AGENTS.md") {
		t.Fatalf("key should mention file: %q", k1)
	}
}
