package skills

import (
	"os"
	"path/filepath"
)

// Candidate is a skill folder installed by another tool that vibe can load.
type Candidate struct {
	Dir   string
	Tool  string
	Count int
}

// knownLocations are the skill folders of popular agent tools, checked in
// this order.
var knownLocations = []struct {
	rel  string
	tool string
}{
	{".agents/skills", "Agents (shared)"},
	{".claude/skills", "Claude Code"},
	{".config/opencode/skills", "OpenCode"},
	{".config/devin/skills", "Devin"},
	{".codex/skills", "Codex"},
}

// DetectCandidates returns the known skill folders that exist under home
// and contain at least one skill, in known-location order.
func DetectCandidates(home string) []Candidate {
	if home == "" {
		return nil
	}
	var out []Candidate
	for _, loc := range knownLocations {
		dir := filepath.Join(home, filepath.FromSlash(loc.rel))
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		if n := CountSkills(dir); n > 0 {
			out = append(out, Candidate{Dir: dir, Tool: loc.tool, Count: n})
		}
	}
	return out
}
