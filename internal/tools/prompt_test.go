package tools

import (
	"strings"
	"testing"
)

func TestRenderGuidelinesBlockEmptyRegistry(t *testing.T) {
	t.Parallel()

	if got := RenderGuidelinesBlock(NewRegistry(), false); got != "" {
		t.Fatalf("expected empty string for empty registry, got %q", got)
	}
	if got := RenderGuidelinesBlock(nil, false); got != "" {
		t.Fatalf("expected empty string for nil registry, got %q", got)
	}
}

func TestRenderXMLProtocolBlockEmptyRegistry(t *testing.T) {
	t.Parallel()

	if got := RenderXMLProtocolBlock(NewRegistry()); got != "" {
		t.Fatalf("expected empty string for empty registry, got %q", got)
	}
	if got := RenderXMLProtocolBlock(nil); got != "" {
		t.Fatalf("expected empty string for nil registry, got %q", got)
	}
}

func TestRenderGuidelinesBlockContent(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.RegisterDefaults()

	out := RenderGuidelinesBlock(reg, false)
	if out == "" {
		t.Fatal("expected non-empty guidelines block")
	}
	if !strings.Contains(out, "# Tool use") {
		t.Fatalf("guidelines missing heading:\n%s", out)
	}
	if !strings.Contains(out, "internet") {
		t.Fatalf("online guidelines should mention internet access:\n%s", out)
	}
}

func TestRenderGuidelinesBlockOffline(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.RegisterDefaults()

	out := RenderGuidelinesBlock(reg, true)
	if strings.Contains(out, "internet") {
		t.Fatalf("offline guidelines must not mention internet access:\n%s", out)
	}
}

func TestRenderXMLProtocolBlockListsRegisteredTools(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.RegisterDefaults()

	out := RenderXMLProtocolBlock(reg)
	if out == "" {
		t.Fatal("expected non-empty protocol block")
	}
	for _, want := range []string{
		"# Tool call format",
		`<invoke name="ToolName">`,
		"Available tools",
		"Read(",
		"Write(",
		"Bash(",
		"Glob(",
		"Grep(",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("protocol block missing %q:\n%s", want, out)
		}
	}
}
