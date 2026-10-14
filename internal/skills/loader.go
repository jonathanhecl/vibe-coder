package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/prompt"
)

// maxSkillFileBytes caps a flat .md skill file (legacy rule).
const maxSkillFileBytes = 50 * 1024

// maxSkillMDBytes caps a SKILL.md manifest (generous: playbooks with
// bundled references can be long).
const maxSkillMDBytes = 256 * 1024

const (
	// maxDescriptionRunes caps the catalog description.
	maxDescriptionRunes = 200
	// maxCatalogEntries caps the catalog rendered into the system prompt.
	maxCatalogEntries = 80
	// maxBundledEntries bounds the file walk so huge skill dirs stay cheap;
	// the Skill tool displays the first 50 and summarizes the rest.
	maxBundledEntries = 500
	// maxBundledDepth bounds how deep Read looks for bundled files.
	maxBundledDepth = 3
)

// Skill is one discoverable playbook: either a standard Agent Skills
// directory (<root>/<name>/SKILL.md) or a legacy flat .md file.
type Skill struct {
	Name        string
	Description string
	Path        string // SKILL.md or flat .md file
	Dir         string // skill base dir for the SKILL.md format; "" for flat files
	Source      string // "project", "user", or "skills_dirs"
}

// Root is one directory scanned for skills.
type Root struct {
	Dir    string
	Source string
}

// Roots returns the directories scanned for skills, in precedence order:
// project roots first, then the user-level config dir, then each
// SKILLS_DIRS entry. The first root wins on a name collision.
func Roots(cfg *config.Config) []Root {
	if cfg == nil {
		return nil
	}
	roots := []Root{
		{Dir: filepath.Join(cfg.Cwd, ".vibe-coder", "skills"), Source: "project"},
		{Dir: filepath.Join(cfg.Cwd, "skills"), Source: "project"},
		{Dir: filepath.Join(cfg.ConfigDir, "skills"), Source: "user"},
	}
	for _, dir := range cfg.SkillsDirs {
		roots = append(roots, Root{Dir: dir, Source: "skills_dirs"})
	}
	return roots
}

// Load discovers every skill under Roots, sorted by name. Earlier roots win
// name collisions.
func Load(cfg *config.Config) []Skill {
	byName := map[string]Skill{}
	for _, root := range Roots(cfg) {
		for _, s := range scanRoot(root) {
			if _, taken := byName[s.Name]; !taken {
				byName[s.Name] = s
			}
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

// CountSkills reports how many skills a single root directory would yield
// under the same rules as Load.
func CountSkills(dir string) int {
	return len(scanRoot(Root{Dir: dir}))
}

// scanRoot applies the discovery rules of one root: flat .md files and
// <subdir>/SKILL.md manifests.
func scanRoot(root Root) []Skill {
	var out []Skill
	entries, err := os.ReadDir(root.Dir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		// SKILL.md format: a subdirectory (os.Stat follows a symlinked
		// skill dir) containing a non-symlink SKILL.md.
		dir := filepath.Join(root.Dir, name)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			manifest := filepath.Join(dir, "SKILL.md")
			info, err := os.Lstat(manifest)
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxSkillMDBytes {
				continue
			}
			data, err := os.ReadFile(manifest)
			if err != nil {
				continue
			}
			fm := parseFrontmatter(string(data))
			skillName := sanitizeName(fm["name"])
			if skillName == "" {
				skillName = name
			}
			desc := sanitizeDescription(fm["description"])
			if desc == "" {
				desc = descriptionFromBody(stripFrontmatter(string(data)))
			}
			out = append(out, Skill{Name: skillName, Description: desc, Path: manifest, Dir: dir, Source: root.Source})
			continue
		}
		// Legacy flat .md file.
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		path := filepath.Join(root.Dir, name)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxSkillFileBytes {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fm := parseFrontmatter(string(data))
		skillName := sanitizeName(fm["name"])
		if skillName == "" {
			skillName = strings.TrimSuffix(name, filepath.Ext(name))
		}
		desc := sanitizeDescription(fm["description"])
		if desc == "" {
			desc = descriptionFromBody(stripFrontmatter(string(data)))
		}
		out = append(out, Skill{Name: skillName, Description: desc, Path: path, Source: root.Source})
	}
	return out
}

// ReadOnlyDirs returns every directory the agent may read without a
// permission prompt: each root, its symlink-resolved path, and each
// discovered skill's base dir (resolved too, since symlinked skill dirs
// point outside their root). Deduped.
func ReadOnlyDirs(cfg *config.Config) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(dir string) {
		if dir == "" {
			return
		}
		if _, ok := seen[dir]; !ok {
			seen[dir] = struct{}{}
			out = append(out, dir)
		}
	}
	for _, root := range Roots(cfg) {
		add(root.Dir)
		if resolved, err := filepath.EvalSymlinks(root.Dir); err == nil {
			add(resolved)
		}
	}
	for _, s := range Load(cfg) {
		if s.Dir == "" {
			continue
		}
		add(s.Dir)
		if resolved, err := filepath.EvalSymlinks(s.Dir); err == nil {
			add(resolved)
		}
	}
	return out
}

// Read loads a skill's full instructions (frontmatter stripped and
// sanitized) plus, for directory-format skills, the bundled files: absolute
// paths of regular files under Dir, hidden entries skipped, at most
// maxBundledDepth levels deep and maxBundledFiles entries (a trailing
// "… N more" marker is appended when truncated).
func Read(s Skill) (body string, files []string, err error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return "", nil, fmt.Errorf("read skill %q: %w", s.Name, err)
	}
	body = prompt.SanitizeInstructions(strings.TrimSpace(stripFrontmatter(string(data))))
	if s.Dir == "" {
		return body, nil, nil
	}
	files = bundledFiles(s.Dir)
	return body, files, nil
}

// bundledFiles walks dir up to maxBundledDepth levels collecting regular
// files. Hidden entries, symlinks and SKILL.md at the root are excluded;
// the walk stops at maxBundledEntries files.
func bundledFiles(dir string) []string {
	var out []string
	var walk func(d string, depth int)
	walk = func(d string, depth int) {
		if len(out) >= maxBundledEntries || depth > maxBundledDepth {
			return
		}
		entries, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			p := filepath.Join(d, name)
			if e.IsDir() {
				walk(p, depth+1)
				continue
			}
			if e.Type()&os.ModeSymlink != 0 {
				continue
			}
			info, err := e.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if depth == 1 && name == "SKILL.md" {
				continue
			}
			abs, err := filepath.Abs(p)
			if err != nil {
				abs = p
			}
			out = append(out, abs)
		}
	}
	walk(dir, 1)
	sort.Strings(out)
	return out
}

// RenderCatalog renders the progressive-disclosure catalog for the system
// prompt: names and descriptions only, with instructions to call the Skill
// tool for the full content.
func RenderCatalog(items []Skill) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Skills\n")
	b.WriteString("Skills are task-specific playbooks. When a task matches a skill's description, " +
		"call the Skill tool with its name before starting and follow its instructions. " +
		"Do not guess a skill's content from its description.\n")
	shown := items
	if len(shown) > maxCatalogEntries {
		shown = shown[:maxCatalogEntries]
	}
	for _, s := range shown {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
	}
	if len(items) > len(shown) {
		fmt.Fprintf(&b, "- … and %d more (call Skill with an unknown name to list all)\n", len(items)-len(shown))
	}
	return strings.TrimRight(b.String(), "\n")
}

// sanitizeName collapses a skill name to a single trimmed line.
func sanitizeName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	return strings.TrimSpace(name)
}

// sanitizeDescription collapses whitespace and caps at maxDescriptionRunes.
func sanitizeDescription(desc string) string {
	desc = strings.Join(strings.Fields(desc), " ")
	runes := []rune(desc)
	if len(runes) > maxDescriptionRunes {
		desc = string(runes[:maxDescriptionRunes]) + "…"
	}
	return desc
}

// descriptionFromBody derives a fallback description from the first
// non-empty body line that is not a heading.
func descriptionFromBody(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return sanitizeDescription(line)
	}
	return ""
}
