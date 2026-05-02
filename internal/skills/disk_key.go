package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// DiskKey fingerprints skill markdown files under the same directories as
// Load, using name, size, and mtime only (no file reads).
func DiskKey(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	dirs := []string{
		filepath.Join(cfg.ConfigDir, "skills"),
		filepath.Join(cfg.Cwd, ".vibe-coder", "skills"),
		filepath.Join(cfg.Cwd, "skills"),
	}
	var b strings.Builder
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") || !strings.HasSuffix(strings.ToLower(name), ".md") {
				continue
			}
			fi, err := entry.Info()
			if err != nil {
				continue
			}
			path := filepath.Join(dir, name)
			fmt.Fprintf(&b, "%s|%d|%d\n", path, fi.ModTime().UnixNano(), fi.Size())
		}
	}
	return b.String()
}
