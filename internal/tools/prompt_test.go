package tools

import (
	"strings"
	"testing"
)

func TestRenderPromptBlockEmptyRegistry(t *testing.T) {
	t.Parallel()

	if got := RenderPromptBlock(NewRegistry()); got != "" {
		t.Fatalf("expected empty string for empty registry, got %q", got)
	}
	if got := RenderPromptBlock(nil); got != "" {
		t.Fatalf("expected empty string for nil registry, got %q", got)
	}
}

func TestRenderPromptBlockListsRegisteredTools(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.RegisterDefaults()

	out := RenderPromptBlock(reg)
	if out == "" {
		t.Fatal("expected non-empty prompt block")
	}
	for _, want := range []string{
		"# Tools",
		`<invoke name="ToolName">`,
		"Available tools:",
		"- Read",
		"- Write",
		"- Bash",
		"- Glob",
		"- Grep",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("prompt block missing %q:\n%s", want, out)
		}
	}
}
