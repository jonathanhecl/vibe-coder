package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

type policyUI struct {
	coverageUI
	asked []string
	deny  string
}

func (u *policyUI) AskPermission(name string, _ map[string]any) tui.Decision {
	u.asked = append(u.asked, name)
	if name == u.deny {
		return tui.DecisionDeny
	}
	return tui.DecisionAllowOnce
}

func TestDelegatedWriteIsAuthorizedAndCheckpointed(t *testing.T) {
	ag := newCoverageAgent(t, fakeClient{}, tui.DecisionAllowOnce, false)
	cmd := exec.Command("git", "init")
	cmd.Dir = ag.cfg.Cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	target := filepath.Join(ag.cfg.Cwd, "file.txt")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	ui := &policyUI{}
	ag.ui = ui
	client := &sequenceClient{replies: []string{`<invoke name="Write">{"file_path":"` + filepath.ToSlash(target) + `","contents":"edited"}</invoke>`, "done"}}
	sub := tools.NewSubAgentTool(ag.cfg, client)
	result, _, err := ag.executeTool(context.Background(), sub, sub.Name(), map[string]any{"prompt": "edit", "allow_writes": true}, toolExecutionMode{})
	if err != nil || result.IsError {
		t.Fatalf("delegation failed: %+v, %v", result, err)
	}
	if strings.Join(ui.asked, ",") != "Write" {
		t.Fatalf("expected child Write approval: %v", ui.asked)
	}
	if err := ag.cp.Rollback(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "original" {
		t.Fatalf("missing child checkpoint: %q, %v", data, err)
	}
}

func TestDelegatedWritesRespectModesAndDenyRules(t *testing.T) {
	for _, mode := range []string{"plan", "review", "deny"} {
		t.Run(mode, func(t *testing.T) {
			ag := newCoverageAgent(t, fakeClient{}, tui.DecisionAllowOnce, true)
			switch mode {
			case "plan":
				ag.EnterPlanMode()
			case "review":
				ag.EnterReviewMode()
			case "deny":
				ag.perm.DenySession("Write")
			}
			target := filepath.Join(ag.cfg.Cwd, "outside.txt")
			client := &coverageClient{reply: `<invoke name="Write">{"file_path":"` + filepath.ToSlash(target) + `","contents":"unexpected"}</invoke>`}
			sub := tools.NewSubAgentTool(ag.cfg, client)
			parallel := tools.NewParallelAgentsTool(sub)
			params := map[string]any{"tasks": []any{map[string]any{"prompt": "one", "allow_writes": true}, map[string]any{"prompt": "two", "allow_writes": true}}}
			result, _, err := ag.executeTool(context.Background(), parallel, parallel.Name(), params, toolExecutionMode{})
			if err != nil || !result.IsError {
				t.Fatalf("expected child block: %+v, %v", result, err)
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("unauthorized file exists: %v", err)
			}
		})
	}
}

func TestDelegatedToolsCannotUseMissingParentTools(t *testing.T) {
	ag := newCoverageAgent(t, fakeClient{}, tui.DecisionAllowOnce, true)
	ag.reg = tools.NewRegistry()
	_, err := ag.executeDelegatedTool(context.Background(), tools.NewWriteTool(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("expected unavailable tool, got %v", err)
	}
}

func TestAutoTestUsesExecutionPermission(t *testing.T) {
	ag := newCoverageAgent(t, fakeClient{}, tui.DecisionAllowOnce, false)
	ui := &policyUI{deny: "Bash"}
	ag.ui = ui
	if err := os.WriteFile(filepath.Join(ag.cfg.Cwd, "go.mod"), []byte("module example.test/fixture\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := ag.reg.Get("Write")
	result, _, err := ag.executeTool(context.Background(), tool, tool.Name(), map[string]any{"file_path": filepath.Join(ag.cfg.Cwd, "main.go"), "contents": "package fixture\n"}, toolExecutionMode{})
	if err != nil || result.IsError {
		t.Fatalf("write failed: %+v, %v", result, err)
	}
	if strings.Join(ui.asked, ",") != "Write,Bash" {
		t.Fatalf("auto-test bypassed execution permission: %v", ui.asked)
	}
	found := false
	for _, msg := range ag.sess.Messages() {
		if strings.Contains(msg.Content, "AUTO-TEST") && strings.Contains(msg.Content, "not approved") {
			found = true
		}
	}
	if !found {
		t.Fatal("missing skipped auto-test observation")
	}
}
