package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/prompt"
)

type Skill struct {
	Name    string
	Content string
}

func Load(cfg *config.Config) []Skill {
	dirs := []string{
		filepath.Join(cfg.ConfigDir, "skills"),
		filepath.Join(cfg.Cwd, ".vibe-coder", "skills"),
		filepath.Join(cfg.Cwd, "skills"),
	}

	byName := map[string]Skill{}
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
			path := filepath.Join(dir, name)
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Size() > 50*1024 {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			content := string(data)
			if len(content) > 2000 {
				content = content[:2000]
			}
			content = prompt.SanitizeInstructions(content)
			base := strings.TrimSuffix(name, filepath.Ext(name))
			byName[base] = Skill{Name: base, Content: strings.TrimSpace(content)}
		}
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Skill, 0, len(names))
	for _, name := range names {
		out = append(out, byName[name])
	}
	return out
}

func RenderBlock(items []Skill) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range items {
		if strings.TrimSpace(s.Content) == "" {
			continue
		}
		b.WriteString("## ")
		b.WriteString(s.Name)
		b.WriteString("\n")
		b.WriteString(s.Content)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}
