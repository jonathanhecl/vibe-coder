package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/skills"
)

// SkillTool loads the full instructions of a discovered skill on demand:
// the system prompt only carries the catalog (name + description), and this
// tool returns the body plus the skill's bundled files.
type SkillTool struct {
	cfg *config.Config
	// maxOutput caps the model-facing output; set via Registry.SetOutputLimit.
	maxOutput int
}

func NewSkillTool(cfg *config.Config) *SkillTool {
	return &SkillTool{cfg: cfg}
}

// SetMaxOutputBytes implements outputLimiter for the shared tool cap.
func (t *SkillTool) SetMaxOutputBytes(n int) { t.maxOutput = n }

func (t *SkillTool) Name() string { return "Skill" }
func (t *SkillTool) Description() string {
	return "Load the full instructions of a skill listed in the Skills catalog. Call it before starting a task that matches a skill's description."
}
func (t *SkillTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "Skill name exactly as listed in the Skills catalog."},
				},
				"required": []string{"name"},
			},
		},
	}
}

func (t *SkillTool) Execute(ctx context.Context, params map[string]any) Result {
	name, _ := params["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return errResult("name is required")
	}
	items := skills.Load(t.cfg)
	var found *skills.Skill
	for i := range items {
		if strings.EqualFold(items[i].Name, name) {
			found = &items[i]
			break
		}
	}
	if found == nil {
		names := make([]string, 0, len(items))
		for i, s := range items {
			if i >= 80 {
				names = append(names, fmt.Sprintf("… and %d more", len(items)-i))
				break
			}
			names = append(names, s.Name)
		}
		if len(names) == 0 {
			return errResult(fmt.Sprintf("unknown skill %q: no skills are installed", name))
		}
		return errResult(fmt.Sprintf("unknown skill %q. Available: %s", name, strings.Join(names, ", ")))
	}

	body, files, err := skills.Read(*found)
	if err != nil {
		return errResult(err.Error())
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Skill: %s (%s: %s)\n", found.Name, found.Source, found.Path)
	if found.Dir != "" {
		fmt.Fprintf(&b, "Base directory: %s\n", found.Dir)
	}
	b.WriteString("\n")
	b.WriteString(body)
	if len(files) > 0 {
		b.WriteString("\n\nBundled files (open with Read when the instructions reference them):\n")
		shown := files
		more := 0
		if len(files) > 50 {
			shown = files[:50]
			more = len(files) - 50
		}
		for _, f := range shown {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		if more > 0 {
			fmt.Fprintf(&b, "- … %d more\n", more)
		}
	}
	out := strings.TrimRight(b.String(), "\n")
	if t.maxOutput > 0 && len(out) > t.maxOutput {
		out = TruncateOutput(out, t.maxOutput) + "\n[skill output truncated to the tool output cap]"
	}
	return Result{Output: out}
}
