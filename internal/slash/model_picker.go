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

// pickModel prints a numbered list of installed models and asks the user to
// choose one, mirroring the first-run wizard: the list is shown once, the
// current value is marked, and Enter leaves the selection unchanged.
//
// command is the slash name used in fallback hints ("model", "sidecar", ...);
// role labels the prompt ("Model", "Sidecar", "Decision"). allowDisable adds a
// [0] entry so the caller can turn the role off.
//
// It returns the chosen model name and whether the selection changed. A disable
// choice returns ("", true).
func (c *Ctx) pickModel(ctx context.Context, command, role, current string, allowDisable bool) (string, bool, error) {
	models := c.installedModels(ctx)
	if len(models) == 0 {
		fmt.Fprintln(c.Out, "No installed models reported by /api/tags.")
	}
	if c.Prompter == nil {
		if len(models) > 0 {
			c.printModelChoices(models, role, current, allowDisable)
		}
		fmt.Fprintf(c.Out, "Use /%s <name> to switch.\n", command)
		return "", false, nil
	}
	if len(models) > 0 {
		c.printModelChoices(models, role, current, allowDisable)
	}

	answer, err := c.Prompter.GetInput(role + " choice: ")
	if err != nil {
		return "", false, err
	}
	answer = strings.TrimSpace(answer)
	switch {
	case answer == "":
		fmt.Fprintf(c.Out, "%s unchanged.\n", role)
		return "", false, nil
	case allowDisable && isDisableChoice(answer):
		return "", true, nil
	}
	if idx, ok := parseModelChoiceIndex(answer, len(models)); ok {
		return models[idx].Name, true, nil
	}
	if !allDigits(answer) && modelNameRe.MatchString(answer) {
		return answer, true, nil
	}
	fmt.Fprintln(c.Out, "Invalid choice. Enter a listed number or a model name.")
	return "", false, nil
}

// printModelChoices renders the numbered list shared by the model pickers and
// the optional [0]/[Enter] entries.
func (c *Ctx) printModelChoices(models []ollama.Model, role, current string, allowDisable bool) {
	c.printModelRows(models, fmt.Sprintf("Installed models (%d)", len(models)), func(m ollama.Model) string {
		if strings.EqualFold(strings.TrimSpace(m.Name), strings.TrimSpace(current)) {
			return "(current)"
		}
		return ""
	})
	st := tui.NewStyle(c.Out)
	if allowDisable {
		fmt.Fprintf(c.Out, "  %s Disable %s\n", st.BoldBrightGreen("[0]"), role)
	}
	fmt.Fprintf(c.Out, "  %s Keep current\n", st.BoldBrightGreen("[Enter]"))
}

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

// resolveModelArg maps a numeric argument to an installed model, or returns it
// unchanged when it is a name. A numeric argument out of range reports false so
// callers can reject it instead of treating the digits as a model name.
func (c *Ctx) resolveModelArg(ctx context.Context, arg string) (string, bool) {
	arg = strings.TrimSpace(arg)
	if !allDigits(arg) {
		return arg, true
	}
	models := c.installedModels(ctx)
	idx, ok := parseModelChoiceIndex(arg, len(models))
	if !ok {
		return "", false
	}
	return models[idx].Name, true
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

func isDisableChoice(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "0", "off", "none", "disable", "disabled":
		return true
	}
	return false
}
