package agent

import (
	"fmt"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func TestZZBaselinePromptSize(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		toolsKnown, toolsSupported bool
	}{
		{"native", true, true},
		{"xml", true, false},
	} {
		tmp := t.TempDir()
		cfg := &config.Config{
			Model:          "m",
			ContextWindow:  8000,
			Cwd:            tmp,
			SessionsDir:    tmp,
			ToolsKnown:     tc.toolsKnown,
			ToolsSupported: tc.toolsSupported,
		}
		sess := session.New(cfg)
		reg := tools.NewRegistry()
		reg.RegisterDefaults()
		perm := permissions.NewManager(&config.Config{YesMode: true})
		ag := New(cfg, fakeClient{}, reg, perm, sess, &fakeUI{})
		ag.currentGoal = "fix the failing test"
		p := ag.buildSystemPrompt()
		fmt.Printf("BASELINE %s system prompt chars=%d est_tokens=%d\n", tc.name, len(p), session.EstimateTokens(p))
	}
	fmt.Printf("BASELINE tool-observation envelope chars=%d\n", len(session.ToolObservationUserContent("Read", "x")))
}
