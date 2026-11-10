package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// TestInputHistorySeed verifies seeding preloads entries in memory, drops
// blanks and consecutive duplicates, and never writes a history file.
func TestInputHistorySeed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "history.txt")
	h := loadHistory(path)

	h.seed([]string{"first prompt", "", "   ", "first prompt", "second prompt"})
	got := h.snapshot()
	want := []string{"first prompt", "second prompt"}
	if len(got) != len(want) {
		t.Fatalf("seeded history = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("seeded history[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("seed must not create a history file, stat err = %v", err)
	}
}

// TestPlainUISeedHistory verifies the concrete UI method reaches the input
// history so Up/Down recall works for sessions without a history file.
func TestPlainUISeedHistory(t *testing.T) {
	t.Parallel()
	u := NewPlain(&config.Config{HistoryFile: ""})
	u.SeedHistory([]string{"alpha", "beta"})
	got := u.history.snapshot()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("SeedHistory did not populate input history: %v", got)
	}
}

// TestRichUISeedHistory verifies the rich UI delegates seeding to the plain
// renderer it wraps.
func TestRichUISeedHistory(t *testing.T) {
	t.Parallel()
	r := NewRich(&config.Config{HistoryFile: ""})
	defer r.Stop()
	r.SeedHistory([]string{"rich prompt"})
	got := r.plain.history.snapshot()
	if len(got) != 1 || got[0] != "rich prompt" {
		t.Fatalf("RichUI SeedHistory did not reach the plain history: %v", got)
	}
}
