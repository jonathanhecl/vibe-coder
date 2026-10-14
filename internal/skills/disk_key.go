package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// DiskKey fingerprints the skill files under every Root — flat .md files
// and <subdir>/SKILL.md manifests — using path, size, and mtime only (no
// file reads), so a change invalidates the cached system prompt.
func DiskKey(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	var b strings.Builder
	for _, root := range Roots(cfg) {
		entries, err := os.ReadDir(root.Dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				manifest := filepath.Join(root.Dir, name, "SKILL.md")
				if fi, err := os.Stat(manifest); err == nil && fi.Mode().IsRegular() {
					fmt.Fprintf(&b, "%s|%d|%d\n", manifest, fi.ModTime().UnixNano(), fi.Size())
				}
				continue
			}
			if !strings.HasSuffix(strings.ToLower(name), ".md") {
				continue
			}
			fi, err := entry.Info()
			if err != nil {
				continue
			}
			path := filepath.Join(root.Dir, name)
			fmt.Fprintf(&b, "%s|%d|%d\n", path, fi.ModTime().UnixNano(), fi.Size())
		}
	}
	return b.String()
}
