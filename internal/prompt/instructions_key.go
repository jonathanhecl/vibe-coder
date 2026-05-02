package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InstructionsDiskKey returns a compact fingerprint of project instruction
// files (.vibe-coder.json, AGENTS.md) along ancestor directories. It mirrors
// loadProjectInstructions' walk but only records path, size, and mtime so
// callers can detect changes without reading file contents.
func InstructionsDiskKey(cwd string) string {
	start, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	var b strings.Builder
	current := start
	for i := 0; i < 10; i++ {
		for _, name := range []string{".vibe-coder.json", "AGENTS.md"} {
			path := filepath.Join(current, name)
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			rel, _ := filepath.Rel(start, path)
			fmt.Fprintf(&b, "%s|%d|%d\n", relPath(rel), info.ModTime().UnixNano(), info.Size())
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return b.String()
}
