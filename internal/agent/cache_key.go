package agent

import (
	"os"
	"runtime"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/prompt"
	"github.com/jonathanhecl/vibe-coder/internal/skills"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// stableSystemCacheKey fingerprints inputs that affect the stable portion of
// the system prompt (base prompt, tools block, skills block).
func stableSystemCacheKey(cfg *config.Config, reg *tools.Registry) string {
	parts := []string{
		runtime.GOOS + "/" + runtime.GOARCH,
		shellEnvFingerprint(),
	}
	if cfg != nil {
		parts = append(parts,
			strings.TrimSpace(cfg.Cwd),
			prompt.InstructionsDiskKey(cfg.Cwd),
			skills.DiskKey(cfg),
		)
	}
	if reg != nil {
		parts = append(parts, strings.Join(reg.Names(), ","))
	}
	return strings.Join(parts, "\x1e")
}

func shellEnvFingerprint() string {
	sh := strings.TrimSpace(os.Getenv("SHELL"))
	cs := strings.TrimSpace(os.Getenv("COMSPEC"))
	return sh + "|" + cs
}
