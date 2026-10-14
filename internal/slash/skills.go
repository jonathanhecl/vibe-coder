package slash

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/skills"
)

// userHomeDir is os.UserHomeDir, replaceable in tests.
var userHomeDir = os.UserHomeDir

// skillsOption is one selectable folder in the /skills set list.
type skillsOption struct {
	Dir   string
	Tool  string
	Count int
}

// runSkillsCommand dispatches /skills subcommands; bare /skills lists.
func runSkillsCommand(c *Ctx, args []string) error {
	if len(args) > 0 && args[0] == "set" {
		return runSkillsSet(c, args[1:])
	}
	items := skills.Load(c.Cfg)
	if len(items) == 0 {
		fmt.Fprintln(c.Out, "No skills found. Add skills under .vibe-coder/skills/ or <configDir>/skills/, or point SKILLS_DIRS at existing skill folders (e.g. ~/.config/opencode/skills). Run /skills set to configure them.")
		return nil
	}
	fmt.Fprintf(c.Out, "Skills (%d):\n", len(items))
	for _, s := range items {
		desc := strings.TrimSpace(s.Description)
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(c.Out, "  %s [%s] — %s\n", s.Name, s.Source, desc)
	}
	// Point at folders from other tools the user has not enabled yet.
	if len(c.Cfg.SkillsDirs) == 0 {
		if home, err := userHomeDir(); err == nil {
			if cands := skills.DetectCandidates(home); len(cands) > 0 {
				tools := make([]string, 0, len(cands))
				for _, cand := range cands {
					tools = append(tools, cand.Tool)
				}
				fmt.Fprintf(c.Out, "Found skill folders from other tools (%s). Run /skills set to choose which ones to load.\n",
					strings.Join(tools, ", "))
			}
		}
	}
	return nil
}

// skillsOptions builds the numbered list: detected candidates first (in
// known-location order), then current SKILLS_DIRS entries that were not
// detected (labeled "custom"), deduped by cleaned absolute path.
func skillsOptions(home string, cfg *config.Config) []skillsOption {
	var out []skillsOption
	seen := map[string]struct{}{}
	for _, cand := range skills.DetectCandidates(home) {
		key := cleanAbs(cand.Dir)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, skillsOption{Dir: cand.Dir, Tool: cand.Tool, Count: cand.Count})
	}
	for _, dir := range cfg.SkillsDirs {
		key := cleanAbs(dir)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, skillsOption{Dir: dir, Tool: "custom", Count: skills.CountSkills(dir)})
	}
	return out
}

func cleanAbs(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(dir)
}

// selectedDirs reports which listed options are already in cfg.SkillsDirs.
func selectedDirs(cfg *config.Config) map[string]bool {
	out := map[string]bool{}
	for _, d := range cfg.SkillsDirs {
		out[cleanAbs(d)] = true
	}
	return out
}

func abbrevHome(home, path string) string {
	if home != "" && (path == home || strings.HasPrefix(path, home+string(filepath.Separator))) {
		return "~" + path[len(home):]
	}
	return path
}

func runSkillsSet(c *Ctx, args []string) error {
	home, err := userHomeDir()
	if err != nil {
		home = ""
	}
	options := skillsOptions(home, c.Cfg)
	selected := selectedDirs(c.Cfg)

	printList := func() {
		if len(options) == 0 {
			fmt.Fprintln(c.Out, "No skill folders detected.")
			return
		}
		fmt.Fprintln(c.Out, "Skill folders (selected ones are loaded):")
		for i, o := range options {
			mark := " "
			if selected[cleanAbs(o.Dir)] {
				mark = "x"
			}
			fmt.Fprintf(c.Out, "  [%s] %d  %-15s %d skills  %s\n", mark, i+1, o.Tool, o.Count, abbrevHome(home, o.Dir))
		}
	}
	printList()

	var input string
	if len(args) > 0 {
		input = strings.Join(args, " ")
	} else if c.Prompter != nil {
		line, err := c.Prompter.GetInput(`Select folders by number (e.g. 1,3), "all", "none", and/or add paths; Enter keeps the current setting: `)
		if err != nil {
			return err
		}
		input = line
	} else {
		fmt.Fprintln(c.Out, "Usage: /skills set <numbers|all|none|path...>")
		return nil
	}

	dirs, warnings, err := parseSkillsSetInput(input, options, c.Cfg)
	if err != nil {
		fmt.Fprintln(c.Out, err.Error())
		return nil
	}
	if dirs == nil {
		fmt.Fprintln(c.Out, "No changes.")
		return nil
	}
	for _, w := range warnings {
		fmt.Fprintln(c.Out, w)
	}

	c.Cfg.SkillsDirs = dirs
	if c.Perm != nil {
		c.Perm.SetReadOnlyRoots(skills.ReadOnlyDirs(c.Cfg))
	}
	if len(dirs) == 0 {
		fmt.Fprintln(c.Out, "Skill folders cleared. Run /save to persist.")
		return nil
	}
	fmt.Fprintf(c.Out, "Skill folders updated (%d):\n", len(dirs))
	for _, d := range dirs {
		fmt.Fprintf(c.Out, "  %s\n", abbrevHome(home, d))
	}
	fmt.Fprintf(c.Out, "Skills available: %d. Run /save to persist.\n", len(skills.Load(c.Cfg)))
	return nil
}

// parseSkillsSetInput turns the /skills set input into the new SkillsDirs.
// nil means "no change" (blank input). Numbered tokens pick options by the
// printed index; "all" picks every option; "none" clears and cannot combine
// with anything else; other tokens are paths that must be existing dirs.
func parseSkillsSetInput(input string, options []skillsOption, cfg *config.Config) (dirs []string, warnings []string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, nil, nil
	}
	fields := strings.FieldsFunc(input, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if len(fields) == 0 {
		return nil, nil, nil
	}

	var picked []string
	var custom []string
	none := false
	for _, f := range fields {
		lower := strings.ToLower(f)
		switch {
		case lower == "all":
			for _, o := range options {
				picked = append(picked, o.Dir)
			}
		case lower == "none":
			none = true
		default:
			if n, convErr := strconv.Atoi(f); convErr == nil {
				if n < 1 || n > len(options) {
					return nil, nil, fmt.Errorf("option %d out of range: pick 1..%d", n, len(options))
				}
				picked = append(picked, options[n-1].Dir)
				continue
			}
			custom = append(custom, f)
		}
	}
	if none {
		if len(picked) > 0 || len(custom) > 0 {
			return nil, nil, fmt.Errorf("\"none\" cannot be combined with other selections")
		}
		return []string{}, nil, nil
	}

	for _, p := range custom {
		norm := config.NormalizeSkillsDirs(cfg.Cwd, []string{p})
		if len(norm) == 0 {
			continue
		}
		dir := norm[0]
		info, statErr := os.Stat(dir)
		if statErr != nil || !info.IsDir() {
			return nil, nil, fmt.Errorf("not an existing directory: %s", p)
		}
		if n := skills.CountSkills(dir); n == 0 {
			warnings = append(warnings, fmt.Sprintf("warning: no skills found under %s", dir))
		}
		picked = append(picked, dir)
	}
	return config.NormalizeSkillsDirs(cfg.Cwd, picked), warnings, nil
}
