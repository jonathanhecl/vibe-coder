package watcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWatcherDetectsExternalChange(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	w := New(tmp)
	defer w.Close()
	w.RefreshSnapshot()
	_ = w.PendingChanges() // start lazy poller

	if err := os.WriteFile(file, []byte("xx"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	time.Sleep(2300 * time.Millisecond)
	changes := w.PendingChanges()
	if len(changes) == 0 {
		t.Fatalf("expected at least one change")
	}
	note := w.Format(changes)
	if !strings.Contains(note, "[System Note]") {
		t.Fatalf("unexpected note: %q", note)
	}
}
