package tui

import (
	"bytes"
	"testing"
)

// fdWriter is a writer that exposes a descriptor without being an *os.File.
type fdWriter struct {
	fd uintptr
}

func (f fdWriter) Write(p []byte) (int, error) { return len(p), nil }
func (f fdWriter) Fd() uintptr                 { return f.fd }

// The width probe must see through writers that forward Fd. The hybrid TUI wraps
// stdout to paint the prompt draft, and asserting on the concrete *os.File hid
// the real terminal width behind that wrapper: every card fell back to 80
// columns and used half of a wide window.
func TestWriterFdSeesThroughWrappers(t *testing.T) {
	t.Parallel()

	base := fdWriter{fd: 7}
	lw := &liveWriter{ui: &PlainUI{}, w: base}
	if fd, ok := writerFd(lw); !ok || fd != 7 {
		t.Fatalf("writerFd(liveWriter) = (%d, %t), want (7, true)", fd, ok)
	}
	if fd, ok := writerFd(base); !ok || fd != 7 {
		t.Fatalf("writerFd(fdWriter) = (%d, %t), want (7, true)", fd, ok)
	}
	if _, ok := writerFd(&bytes.Buffer{}); ok {
		t.Fatal("a buffer has no descriptor")
	}
}
