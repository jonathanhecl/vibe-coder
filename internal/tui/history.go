package tui

import (
	"bufio"
	"os"
	"strings"
	"sync"
)

// maxHistoryEntries caps the in-memory and on-disk history so a long-running
// session cannot grow the file without bound.
const maxHistoryEntries = 1000

// inputHistory stores submitted user inputs so Up/Down can recall them across
// sessions. Entries are persisted to a file (one line per entry) and kept in
// memory for fast navigation.
type inputHistory struct {
	mu      sync.Mutex
	entries []string
	path    string
}

// loadHistory reads any existing entries from path. A missing file is not an
// error: it simply yields an empty history.
func loadHistory(path string) *inputHistory {
	h := &inputHistory{path: path}
	if path == "" {
		return h
	}
	f, err := os.Open(path)
	if err != nil {
		return h
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line != "" {
			h.entries = append(h.entries, line)
		}
	}
	// A non-EOF error (e.g. a line exceeding the buffer) means the file is
	// truncated; keep whatever we managed to read rather than failing input.
	_ = scanner.Err()
	return h
}

// add appends line to the history unless it is empty or identical to the most
// recent entry. It also appends to the on-disk file so history survives restarts.
func (h *inputHistory) add(line string) {
	if h == nil || strings.TrimSpace(line) == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == line {
		return
	}
	h.entries = append(h.entries, line)
	if len(h.entries) > maxHistoryEntries {
		h.entries = h.entries[len(h.entries)-maxHistoryEntries:]
	}
	h.appendLocked(line)
}

// appendLocked writes line as a new line in the history file. Errors are
// ignored: history is a convenience feature and must never break input.
func (h *inputHistory) appendLocked(line string) {
	if h.path == "" {
		return
	}
	f, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	// Replace newlines so a single logical entry stays on one disk line.
	_, _ = f.WriteString(strings.ReplaceAll(line, "\n", " "))
	_, _ = f.WriteString("\n")
}

// snapshot returns a copy of the current entries for navigation.
func (h *inputHistory) snapshot() []string {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.entries))
	copy(out, h.entries)
	return out
}
