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
				_, executed, err := ag.executeTool(context.Background(), probe, name, map[string]any{}, toolExecutionMode{})
				if err != nil || executed || probe.called {
					t.Fatalf("restricted tool executed: executed=%t called=%t err=%v", executed, probe.called, err)
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
				_, executed, err := ag.executeTool(context.Background(), tool, name, map[string]any{"file_path": path, "contents": "changed", "old_string": "original", "new_string": "changed", "command": "echo unexpected"}, toolExecutionMode{})
				if err != nil || executed {
					t.Fatalf("%s allowed in %s: executed=%t err=%v", name, mode, executed, err)
				}
			}
			read := ag.reg.Get("Read")
			result, executed, err := ag.executeTool(context.Background(), read, read.Name(), map[string]any{"file_path": path}, toolExecutionMode{})
			if err != nil || !executed || result.IsError {
				t.Fatalf("read was blocked: %+v, %v", result, err)
			}
			plan := filepath.Join(ag.cfg.Cwd, ".vibe-coder", "plans", "proposal.md")
			write := ag.reg.Get("Write")
			result, executed, err = ag.executeTool(context.Background(), write, write.Name(), map[string]any{"file_path": plan, "contents": "plan"}, toolExecutionMode{})
			if err != nil || executed != (mode == "plan") {
				t.Fatalf("plan write policy mismatch: executed=%t err=%v", executed, err)
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
