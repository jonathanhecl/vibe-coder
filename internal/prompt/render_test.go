package prompt

import (
	"strings"
	"testing"
)

func TestRenderAllTemplates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		data any
		want []string
	}{
		{"base", nil, []string{"autonomous local-first coding agent"}},
		{"tool_guidelines", struct{ Offline bool }{Offline: false}, []string{"# Tool use"}},
		{"tool_guidelines", struct{ Offline bool }{Offline: true}, []string{"# Tool use"}},
		{"tool_protocol_xml", struct{ Tools []string }{Tools: []string{"Read(file_path): read a file"}}, []string{`<invoke name="ToolName">`, "Read(file_path): read a file"}},
		{"goal", struct{ Goal string }{Goal: "fix it"}, []string{"<<<USER_GOAL>>>", "fix it", "<<<END_USER_GOAL>>>"}},
		{"plan_mode", nil, []string{"# Plan Mode"}},
		{"review_mode", nil, []string{"# Review Mode"}},
		{"offline_mode", nil, []string{"# Offline Mode"}},
		{"mission", struct{ Goal string }{Goal: "ship it"}, []string{"# Active Mission", "ship it"}},
		{"mission", struct{ Goal string }{}, []string{"# Active Mission"}},
		{"compaction_summary", nil, []string{"Summarize the earlier portion"}},
	}
	for _, tc := range cases {
		out := Render(tc.name, tc.data)
		if strings.TrimSpace(out) == "" {
			t.Fatalf("%s: expected non-empty render", tc.name)
		}
		for _, want := range tc.want {
			if !strings.Contains(out, want) {
				t.Fatalf("%s: missing %q in:\n%s", tc.name, want, out)
			}
		}
	}
}

func TestRenderToolGuidelinesOffline(t *testing.T) {
	t.Parallel()

	online := Render("tool_guidelines", struct{ Offline bool }{Offline: false})
	if !strings.Contains(online, "internet") {
		t.Fatal("online guidelines should mention internet access")
	}
	offline := Render("tool_guidelines", struct{ Offline bool }{Offline: true})
	if strings.Contains(offline, "internet") {
		t.Fatal("offline guidelines must not mention internet access")
	}
}

func TestRenderUnknownNamePanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for unknown template")
		}
	}()
	Render("does_not_exist", nil)
}

func TestRenderTrimsOutput(t *testing.T) {
	t.Parallel()

	out := Render("base", nil)
	if out != strings.TrimSpace(out) {
		t.Fatal("expected trimmed output")
	}
}
