package slash

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// printModelRows prints the numbered model list under a title, with the
// capability tags colored per capability, appending the annotation returned by
// suffixFor to each row.
func (c *Ctx) printModelRows(models []ollama.Model, title string, suffixFor func(ollama.Model) string) {
	st := tui.NewStyle(c.Out)
	fmt.Fprintln(c.Out, st.BoldCyan(title))
	for i, m := range models {
		line := fmt.Sprintf("  %s %s", st.BoldBrightGreen(fmt.Sprintf("[%d]", i+1)), m.Name)
		if tags := capabilityTagsFor(m, c.Cfg); len(tags) > 0 {
			line += "  " + strings.Join(coloredCapabilityTags(st, tags), st.Dim(" · "))
		}
		if suffix := suffixFor(m); suffix != "" {
			line += "  " + st.Dim(suffix)
		}
		fmt.Fprintln(c.Out, line)
	}
}

// installedModels lists the host's models, falling back to the names cached at
// startup when the live /api/tags call is unavailable.
func (c *Ctx) installedModels(ctx context.Context) []ollama.Model {
	if c.Models != nil {
		listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if models, err := c.Models.Tags(listCtx); err == nil && len(models) > 0 {
			return models
		}
	}
	return c.cachedModels()
}

// cachedModels rebuilds a sorted model list from the capability maps filled at
// startup. It keeps the pickers usable without a network round-trip.
func (c *Ctx) cachedModels() []ollama.Model {
	seen := map[string]struct{}{}
	collect := func(m map[string]bool) {
		for name := range m {
			if name = strings.TrimSpace(name); name != "" {
				seen[strings.ToLower(name)] = struct{}{}
			}
		}
	}
	if c.Cfg != nil {
		collect(c.Cfg.VisionByModel)
		collect(c.Cfg.ToolsByModel)
		collect(c.Cfg.ThinkingByModel)
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	models := make([]ollama.Model, 0, len(names))
	for _, name := range names {
		models = append(models, ollama.Model{Name: name})
	}
	return models
}

func parseModelChoiceIndex(raw string, total int) (int, bool) {
	if total <= 0 {
		return 0, false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || !allDigits(raw) {
		return 0, false
	}
	n := 0
	for _, r := range raw {
		n = n*10 + int(r-'0')
	}
	if n <= 0 || n > total {
		return 0, false
	}
	return n - 1, true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
