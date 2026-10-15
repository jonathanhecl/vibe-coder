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

func TestEffectOfClassifiesReadManyAndBackgroundTools(t *testing.T) {
	t.Parallel()

	if got := EffectOf(&ReadManyTool{}); got != EffectRead {
		t.Fatalf("ReadManyTool effect = %v, want EffectRead", got)
	}
	if got := EffectOf(&BackgroundStatusTool{}); got != EffectRead {
		t.Fatalf("BackgroundStatusTool effect = %v, want EffectRead", got)
	}
	if got := EffectOf(&BackgroundListTool{}); got != EffectRead {
		t.Fatalf("BackgroundListTool effect = %v, want EffectRead", got)
	}
	if got := EffectOf(&BackgroundRunTool{}); got != EffectExecute {
		t.Fatalf("BackgroundRunTool effect = %v, want EffectExecute", got)
	}
	if got := EffectOf(&BackgroundKillTool{}); got != EffectExecute {
		t.Fatalf("BackgroundKillTool effect = %v, want EffectExecute", got)
	}
}

func TestRenderXMLProtocolBlockForModeFiltersTools(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.RegisterDefaults()

	reviewOut := RenderXMLProtocolBlockForMode(reg, false, true, false)
	for _, want := range []string{"- Read(", "- ReadMany(", "- Glob(", "- Grep(", "- BackgroundStatus(", "- BackgroundList("} {
		if !strings.Contains(reviewOut, want) {
			t.Fatalf("review XML block missing allowed tool %q:\n%s", want, reviewOut)
		}
	}
	for _, forbidden := range []string{"- Write(", "- Edit(", "- Bash(", "- BackgroundRun(", "- BackgroundKill(", "- GitUndo("} {
		if strings.Contains(reviewOut, forbidden) {
			t.Fatalf("review XML block leaked forbidden tool %q:\n%s", forbidden, reviewOut)
		}
	}

	planOut := RenderXMLProtocolBlockForMode(reg, true, false, false)
	for _, want := range []string{"- Write(", "- Edit("} {
		if !strings.Contains(planOut, want) {
			t.Fatalf("plan XML block should retain %s for .vibe-coder/plans:\n%s", want, planOut)
		}
	}
	for _, forbidden := range []string{"- NotebookEdit(", "- Bash(", "- BackgroundRun(", "- GitUndo("} {
		if strings.Contains(planOut, forbidden) {
			t.Fatalf("plan XML block leaked forbidden tool %q:\n%s", forbidden, planOut)
		}
	}

	offlineOut := RenderXMLProtocolBlockForMode(reg, false, false, true)
	for _, forbidden := range []string{"- WebFetch(", "- WebSearch("} {
		if strings.Contains(offlineOut, forbidden) {
			t.Fatalf("offline XML block leaked network tool %q:\n%s", forbidden, offlineOut)
		}
	}
}
