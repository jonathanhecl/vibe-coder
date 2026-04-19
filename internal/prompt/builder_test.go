package prompt

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestBuildIncludesEnvironmentBlock(t *testing.T) {
	cfg := &config.Config{
		Cwd: "/tmp/project",
	}
	t.Setenv("SHELL", "/bin/bash")

	got := Build(cfg)
	for _, want := range []string{
		"You are vibe-coder",
		"# Environment",
		"- cwd: /tmp/project",
		"- shell: /bin/bash",
		"# OS Notes",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt should contain %q, got:\n%s", want, got)
		}
	}
}

