package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

type policyProbe struct {
	tools.Tool
	name   string
	called bool
}

func (p *policyProbe) Name() string { return p.name }
func (p *policyProbe) Execute(context.Context, map[string]any) tools.Result {
	p.called = true
	return tools.Result{Output: "executed"}
}

func TestRestrictedModesDenyUnknownEffects(t *testing.T) {
	for _, mode := range []string{"plan", "review"} {
		for _, name := range []string{"Edit", "Bash", "NotebookEdit", "GitUndo", "mcp_server_modify", "Read"} {
			t.Run(mode+"/"+name, func(t *testing.T) {
				ag := newCoverageAgent(t, &coverageClient{reply: "done"}, tui.DecisionAllowOnce, true)
				if mode == "plan" {
					ag.EnterPlanMode()
				} else {
					ag.EnterReviewMode()
				}
				probe := &policyProbe{name: name}
				_, outcome, err := ag.executeTool(context.Background(), probe, name, map[string]any{}, toolExecutionMode{})
				if err != nil || outcome.ran() || probe.called {
					t.Fatalf("restricted tool executed: outcome=%v called=%t err=%v", outcome, probe.called, err)
				}
			})
		}
	}
}

func TestRestrictedModesCheckBuiltins(t *testing.T) {
	for _, mode := range []string{"plan", "review"} {
		t.Run(mode, func(t *testing.T) {
			ag := newCoverageAgent(t, &coverageClient{reply: "done"}, tui.DecisionAllowOnce, true)
			if mode == "plan" {
				ag.EnterPlanMode()
			} else {
				ag.EnterReviewMode()
			}
			path := filepath.Join(ag.cfg.Cwd, "outside.txt")
			if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"Write", "Edit", "NotebookEdit", "Bash", "InteractiveBash", "SendInput", "TerminateSession", "GitUndo"} {
				tool := ag.reg.Get(name)
				_, outcome, err := ag.executeTool(context.Background(), tool, name, map[string]any{"file_path": path, "contents": "changed", "old_string": "original", "new_string": "changed", "command": "echo unexpected"}, toolExecutionMode{})
				if err != nil || outcome.ran() {
					t.Fatalf("%s allowed in %s: outcome=%v err=%v", name, mode, outcome, err)
				}
			}
			read := ag.reg.Get("Read")
			result, outcome, err := ag.executeTool(context.Background(), read, read.Name(), map[string]any{"file_path": path}, toolExecutionMode{})
			if err != nil || !outcome.ran() || result.IsError {
				t.Fatalf("read was blocked: %+v, %v", result, err)
			}
			plan := filepath.Join(ag.cfg.Cwd, ".vibe-coder", "plans", "proposal.md")
			write := ag.reg.Get("Write")
			result, outcome, err = ag.executeTool(context.Background(), write, write.Name(), map[string]any{"file_path": plan, "contents": "plan"}, toolExecutionMode{})
			if err != nil || outcome.ran() != (mode == "plan") {
				t.Fatalf("plan write policy mismatch: outcome=%v err=%v", outcome, err)
			}
			if mode == "plan" && result.IsError {
				t.Fatalf("plan write failed: %+v", result)
			}
		})
	}
}

func TestPlanPathChecksHaveNoSideEffects(t *testing.T) {
	ag := newCoverageAgent(t, &coverageClient{reply: "done"}, tui.DecisionDeny, false)
	ag.EnterPlanMode()
	plan := filepath.Join(ag.cfg.Cwd, ".vibe-coder", "plans", "nested", "proposal.md")
	if !ag.isWriteAllowedInPlan(map[string]any{"file_path": plan}) {
		t.Fatal("expected a valid plan path")
	}
	if _, err := os.Stat(filepath.Join(ag.cfg.Cwd, ".vibe-coder")); !os.IsNotExist(err) {
		t.Fatalf("path check created directories: %v", err)
	}
	for _, path := range []string{filepath.Join(ag.cfg.Cwd, ".vibe-coder", "plans"), filepath.Join(ag.cfg.Cwd, ".vibe-coder", "plans-other", "x.md"), filepath.Join(".vibe-coder", "plans", "..", "outside.md")} {
		if ag.isWriteAllowedInPlan(map[string]any{"file_path": path}) {
			t.Fatalf("accepted outside path %s", path)
		}
	}
	root := filepath.Join(ag.cfg.Cwd, ".vibe-coder", "plans")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if ag.isWriteAllowedInPlan(map[string]any{"file_path": filepath.Join(root, "escape", "new.md")}) {
		t.Fatal("accepted symlink ancestor")
	}
}

func TestDelegatedWriteUsesParentPermissions(t *testing.T) {
	ag := newCoverageAgent(t, &coverageClient{reply: "done"}, tui.DecisionDeny, false)
	target := filepath.Join(ag.cfg.Cwd, "denied.txt")
	client := &coverageClient{reply: `<invoke name="Write">{"file_path":"` + filepath.ToSlash(target) + `","contents":"unexpected"}</invoke>`}
	sub := tools.NewSubAgentTool(ag.cfg, client)
	result, _, err := ag.executeTool(context.Background(), sub, sub.Name(), map[string]any{"prompt": "write", "allow_writes": true}, toolExecutionMode{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected delegated permission denial, got %+v", result)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("unauthorized file exists: %v", err)
	}
}

func TestNativeChatToolsAndXMLPromptFilterByMode(t *testing.T) {
	ag := newCoverageAgent(t, &coverageClient{reply: "done"}, tui.DecisionAllowOnce, true)
	ag.cfg.ToolsKnown = true
	ag.cfg.ToolsSupported = true

	fullCount := len(ag.nativeChatTools())
	if fullCount == 0 {
		t.Fatal("expected non-empty native tools in normal mode")
	}

	ag.EnterReviewMode()
	reviewTools := ag.nativeChatTools()
	if len(reviewTools) >= fullCount {
		t.Fatalf("expected review mode to filter tools: got %d, full %d", len(reviewTools), fullCount)
	}
	reviewNames := make(map[string]bool, len(reviewTools))
	for _, td := range reviewTools {
		reviewNames[td.Function.Name] = true
	}
	for _, allowed := range []string{"Read", "ReadMany", "Glob", "Grep", "BackgroundStatus", "BackgroundList"} {
		if !reviewNames[allowed] {
			t.Fatalf("review mode omitted read-only tool %s", allowed)
		}
	}
	for _, forbidden := range []string{"Write", "Edit", "Bash", "BackgroundRun", "BackgroundKill", "GitUndo"} {
		if reviewNames[forbidden] {
			t.Fatalf("review mode leaked mutating/exec tool %s", forbidden)
		}
	}
	ag.ExitReviewMode()

	ag.EnterPlanMode()
	planTools := ag.nativeChatTools()
	planNames := make(map[string]bool, len(planTools))
	for _, td := range planTools {
		planNames[td.Function.Name] = true
	}
	if !planNames["Write"] || !planNames["Edit"] {
		t.Fatal("plan mode should keep Write and Edit tool schemas for plan files")
	}
	for _, forbidden := range []string{"NotebookEdit", "Bash", "BackgroundRun", "GitUndo"} {
		if planNames[forbidden] {
			t.Fatalf("plan mode leaked forbidden tool %s", forbidden)
		}
	}
	ag.ExitPlanMode()

	ag.perm.SetOfflineMode(true)
	offlineTools := ag.nativeChatTools()
	for _, td := range offlineTools {
		if td.Function.Name == "WebFetch" || td.Function.Name == "WebSearch" {
			t.Fatalf("offline mode leaked network tool %s", td.Function.Name)
		}
	}
}
