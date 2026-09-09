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
// the system prompt (base prompt, pinned session context, vision line, tools
// block, skills block). The model name is included because the vision line
// (and therefore the stable body) depends on the active model.
func stableSystemCacheKey(cfg *config.Config, reg *tools.Registry, contextFingerprint string) string {
	parts := []string{
		runtime.GOOS + "/" + runtime.GOARCH,
		shellEnvFingerprint(),
		strings.TrimSpace(contextFingerprint),
	}
	if cfg != nil {
		parts = append(parts,
			strings.TrimSpace(cfg.Cwd),
			visionFingerprint(cfg),
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

// visionFingerprint covers the vision line inputs so model switches and
// capability updates invalidate the cached system prompt.
func visionFingerprint(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	available, known := "0", "0"
	if cfg.VisionAvailable {
		available = "1"
	}
	if cfg.VisionKnown {
		known = "1"
	}
	return strings.TrimSpace(cfg.Model) + "|" + available + known
}
