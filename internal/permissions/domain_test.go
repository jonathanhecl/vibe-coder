package permissions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func TestDomain_IsInsideDomain(t *testing.T) {
	ws := t.TempDir()
	m := NewManager(&config.Config{Cwd: ws})

	// Inside workspace
	if !m.IsInsideDomain(ws) {
		t.Errorf("workspace dir itself should be in domain")
	}
	if !m.IsInsideDomain(filepath.Join(ws, "file.txt")) {
		t.Errorf("file inside workspace should be in domain")
	}
	if !m.IsInsideDomain(filepath.Join(ws, "sub", "dir", "file.txt")) {
		t.Errorf("nested file inside workspace should be in domain")
	}
	if !m.IsInsideDomain("file.txt") {
		t.Errorf("relative path inside workspace should be in domain")
	}
	if !m.IsInsideDomain("sub/dir") {
		t.Errorf("relative subdirectory inside workspace should be in domain")
	}
	if !m.IsInsideDomain("") {
		t.Errorf("empty path should default to in domain")
	}

	// Outside workspace
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "other.txt")
	if m.IsInsideDomain(outsideFile) {
		t.Errorf("file in another dir should be out of domain")
	}
	if m.IsInsideDomain(outsideDir) {
		t.Errorf("another dir should be out of domain")
	}
	if m.IsInsideDomain("../escaping.txt") {
		t.Errorf("relative path escaping workspace should be out of domain")
	}

	// Symlink escaping workspace
	symlinkPath := filepath.Join(ws, "symlink_out")
	if err := os.Symlink(outsideDir, symlinkPath); err == nil {
		if m.IsInsideDomain(filepath.Join(symlinkPath, "file.txt")) {
			t.Errorf("symlink target escaping workspace should be out of domain")
		}
	}
}

func TestDomain_SafeTools_RequirePermissionWhenOutDomain(t *testing.T) {
	ws := t.TempDir()
	outsideDir := t.TempDir()
	m := NewManager(&config.Config{Cwd: ws, YesMode: false})

	inDomainFile := filepath.Join(ws, "local.txt")
	outDomainFile := filepath.Join(outsideDir, "external.txt")

	// Read in-domain is safe (allowed without UI)
	if !m.Check("Read", map[string]any{"file_path": inDomainFile}, nil) {
		t.Fatalf("in-domain Read should be safe and allowed by default")
	}

	// Read out-of-domain requires permission
	if m.Check("Read", map[string]any{"file_path": outDomainFile}, nil) {
		t.Fatalf("out-of-domain Read should be denied without UI")
	}

	// Glob in-domain is safe
	if !m.Check("Glob", map[string]any{"path": ws}, nil) {
		t.Fatalf("in-domain Glob should be safe and allowed by default")
	}
	if !m.Check("Glob", map[string]any{}, nil) {
		t.Fatalf("default Glob (no path) should be in-domain and allowed")
	}

	// Glob out-of-domain requires permission
	if m.Check("Glob", map[string]any{"path": outsideDir}, nil) {
		t.Fatalf("out-of-domain Glob should be denied without UI")
	}

	// Grep in-domain is safe
	if !m.Check("Grep", map[string]any{"path": ws}, nil) {
		t.Fatalf("in-domain Grep should be safe and allowed by default")
	}

	// Grep out-of-domain requires permission
	if m.Check("Grep", map[string]any{"path": outsideDir}, nil) {
		t.Fatalf("out-of-domain Grep should be denied without UI")
	}

	// DescribeImage out-of-domain requires permission
	if m.Check("DescribeImage", map[string]any{"file_path": outDomainFile}, nil) {
		t.Fatalf("out-of-domain DescribeImage should be denied without UI")
	}
}

func TestDomain_OutDomain_AllowOnce(t *testing.T) {
	ws := t.TempDir()
	outsideDir := t.TempDir()
	outDomainFile := filepath.Join(outsideDir, "external.txt")
	m := NewManager(&config.Config{Cwd: ws, YesMode: false})

	// First call with AllowOnce: succeeds
	ui := fakeUI{decision: tui.DecisionAllowOnce}
	if !m.Check("Read", map[string]any{"file_path": outDomainFile}, ui) {
		t.Fatalf("out-of-domain Read should succeed with DecisionAllowOnce")
	}

	// Second call without UI: must be denied because AllowOnce did not persist to session
	if m.Check("Read", map[string]any{"file_path": outDomainFile}, nil) {
		t.Fatalf("subsequent out-of-domain Read after AllowOnce should ask again and fail without UI")
	}
}

func TestDomain_OutDomain_AllowSession(t *testing.T) {
	ws := t.TempDir()
	outsideDir := t.TempDir()
	outDomainFile1 := filepath.Join(outsideDir, "project", "file1.txt")
	outDomainFile2 := filepath.Join(outsideDir, "project", "file2.txt")
	_ = os.MkdirAll(filepath.Dir(outDomainFile1), 0o755)

	m := NewManager(&config.Config{Cwd: ws, YesMode: false})

	// First call with AllowSession: succeeds
	ui := fakeUI{decision: tui.DecisionAllowSession}
	if !m.Check("Read", map[string]any{"file_path": outDomainFile1}, ui) {
		t.Fatalf("out-of-domain Read should succeed with DecisionAllowSession")
	}

	// Same file without UI now succeeds
	if !m.Check("Read", map[string]any{"file_path": outDomainFile1}, nil) {
		t.Fatalf("subsequent Read of same out-of-domain file should be allowed in session")
	}

	// Sibling file in the same non-system directory now succeeds without UI
	if !m.Check("Read", map[string]any{"file_path": outDomainFile2}, nil) {
		t.Fatalf("sibling file in approved out-of-domain dir should be allowed in session")
	}

	// Completely unrelated out-of-domain file is NOT approved
	unrelatedFile := filepath.Join(t.TempDir(), "unrelated.txt")
	if m.Check("Read", map[string]any{"file_path": unrelatedFile}, nil) {
		t.Fatalf("unrelated out-of-domain file should still require permission")
	}
}

func TestDomain_OutDomain_DenySession(t *testing.T) {
	ws := t.TempDir()
	outsideDir := t.TempDir()
	outDomainFile := filepath.Join(outsideDir, "blocked.txt")
	m := NewManager(&config.Config{Cwd: ws, YesMode: false})

	// Deny session
	uiDeny := fakeUI{decision: tui.DecisionDenySession}
	if m.Check("Read", map[string]any{"file_path": outDomainFile}, uiDeny) {
		t.Fatalf("out-of-domain Read should be denied")
	}

	// Even if UI now says AllowOnce, session block takes precedence
	uiAllow := fakeUI{decision: tui.DecisionAllowOnce}
	if m.Check("Read", map[string]any{"file_path": outDomainFile}, uiAllow) {
		t.Fatalf("out-of-domain Read should remain blocked for session")
	}
}

func TestDomain_OutDomain_Persistence(t *testing.T) {
	ws := t.TempDir()
	outsideDir := t.TempDir()
	outDomainFile := filepath.Join(outsideDir, "persisted.txt")
	cfgPath := filepath.Join(t.TempDir(), "vibe-coder.env")

	m := NewManager(&config.Config{Cwd: ws, PermFile: cfgPath, YesMode: false})

	// Allow persistently
	ui := fakeUI{decision: tui.DecisionAllowPersistent}
	if !m.Check("Read", map[string]any{"file_path": outDomainFile}, ui) {
		t.Fatalf("expected persistent allow to succeed")
	}

	// Create a new manager loading the same config file
	m2 := NewManager(&config.Config{Cwd: ws, PermFile: cfgPath, YesMode: false})
	if !m2.Check("Read", map[string]any{"file_path": outDomainFile}, nil) {
		t.Fatalf("new manager should load persistent out-of-domain allow rule")
	}
}

func TestDomain_InDomainAllowSession_DoesNotBypassOutDomain(t *testing.T) {
	ws := t.TempDir()
	outsideDir := t.TempDir()
	inDomainFile := filepath.Join(ws, "app.txt")
	outDomainFile := filepath.Join(outsideDir, "system.txt")

	m := NewManager(&config.Config{Cwd: ws, YesMode: false})

	// Grant in-domain session approval for Write
	ui := fakeUI{decision: tui.DecisionAllowSession}
	if !m.Check("Write", map[string]any{"file_path": inDomainFile, "contents": "hello"}, ui) {
		t.Fatalf("in-domain Write should succeed with AllowSession")
	}

	// In-domain write now succeeds without UI
	if !m.Check("Write", map[string]any{"file_path": inDomainFile, "contents": "hello 2"}, nil) {
		t.Fatalf("subsequent in-domain Write should succeed")
	}

	// Out-of-domain Write MUST NOT be auto-approved by in-domain allow
	if m.Check("Write", map[string]any{"file_path": outDomainFile, "contents": "malicious"}, nil) {
		t.Fatalf("out-of-domain Write must NOT be auto-approved by in-domain session permission")
	}
}

func TestDomain_UserProvidedPath_BypassesOutDomainPrompt(t *testing.T) {
	ws := t.TempDir()
	outsideDir := t.TempDir()
	pastedScreenshot := filepath.Join(outsideDir, "vibe_clipboard_456.png")

	m := NewManager(&config.Config{Cwd: ws, YesMode: false})
	m.NoteUserProvidedPath(pastedScreenshot)

	// Reading a user-provided file outside the workspace succeeds without prompt
	if !m.Check("Read", map[string]any{"file_path": pastedScreenshot}, nil) {
		t.Fatalf("user-provided out-of-domain file should be permitted without UI prompt")
	}
}

func TestDomain_YesModeAndUnattended(t *testing.T) {
	ws := t.TempDir()
	outsideDir := t.TempDir()
	outDomainFile := filepath.Join(outsideDir, "outside.txt")

	// YesMode auto-approves
	mYes := NewManager(&config.Config{Cwd: ws, YesMode: true})
	if !mYes.Check("Read", map[string]any{"file_path": outDomainFile}, nil) {
		t.Fatalf("yes-mode should auto-approve out-of-domain access")
	}

	// Unattended auto-approves
	mUnattended := NewManager(&config.Config{Cwd: ws, YesMode: false})
	mUnattended.SetUnattended(true)
	if !mUnattended.Check("Read", map[string]any{"file_path": outDomainFile}, nil) {
		t.Fatalf("unattended mode should auto-approve out-of-domain access")
	}
}

func TestDomain_SystemDirNotBroadened(t *testing.T) {
	ws := t.TempDir()
	m := NewManager(&config.Config{Cwd: ws, YesMode: false})

	// /etc/hosts is in system dir /etc
	m.AllowOutDomainSession("/etc/hosts")

	if !m.IsOutDomainAllowed("/etc/hosts") {
		t.Fatalf("/etc/hosts should be allowed")
	}
	// /etc/shadow must NOT be automatically allowed just because /etc/hosts was approved
	if m.IsOutDomainAllowed("/etc/shadow") {
		t.Fatalf("/etc/shadow should NOT be allowed when only /etc/hosts was approved")
	}
}

func TestDomain_BashCommand_OutDomainDetection(t *testing.T) {
	ws := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.txt")
	_ = os.WriteFile(outsideFile, []byte("hello"), 0o644)

	m := NewManager(&config.Config{Cwd: ws, YesMode: false})

	// 1. In-domain commands: require normal prompt or assisted mode, but are NOT flagged as out-of-domain
	inDomainCmds := []string{
		"git status",
		"go test ./...",
		"curl -s https://example.com/data",
		"find . -name '*.go'",
		"echo hello > local.txt",
		"cat internal/../internal/tools/bash.go",
	}
	for _, cmd := range inDomainCmds {
		paths := ExtractCommandTargetPaths(cmd, ws)
		if len(paths) > 0 {
			t.Errorf("command %q should not target out-of-domain paths, got %v", cmd, paths)
		}
	}

	// 2. Out-of-domain commands: MUST be flagged as out-of-domain
	outDomainCmds := []struct {
		cmd  string
		want string
	}{
		{"cat " + outsideFile, outsideFile},
		{"head -n 20 " + outsideFile, outsideFile},
		{"ls -la " + outsideDir, outsideDir},
		{"cd " + outsideDir + " && ls", outsideDir},
		{"echo hi > " + filepath.Join(outsideDir, "out.txt"), filepath.Join(outsideDir, "out.txt")},
		{"cat ../escaping.txt", filepath.Clean(filepath.Join(ws, "../escaping.txt"))},
	}
	for _, tc := range outDomainCmds {
		paths := ExtractCommandTargetPaths(tc.cmd, ws)
		if len(paths) == 0 {
			t.Errorf("command %q should be flagged as out-of-domain, got none", tc.cmd)
		} else if paths[0] != tc.want {
			t.Errorf("command %q expected path %q, got %q", tc.cmd, tc.want, paths[0])
		}

		// Check() must deny without UI prompt
		if m.Check("Bash", map[string]any{"command": tc.cmd}, nil) {
			t.Errorf("Check for out-of-domain command %q should fail without UI", tc.cmd)
		}
	}

	// 3. Granting session allow to an in-domain Bash command does NOT auto-approve out-of-domain Bash command
	uiSession := fakeUI{decision: tui.DecisionAllowSession}
	if !m.Check("Bash", map[string]any{"command": "git status"}, uiSession) {
		t.Fatalf("expected git status to be allowed with session decision")
	}
	// In-domain command now succeeds without UI
	if !m.Check("Bash", map[string]any{"command": "git status"}, nil) {
		t.Fatalf("subsequent git status should succeed without UI")
	}
	// But out-of-domain command STILL fails without UI!
	if m.Check("Bash", map[string]any{"command": "cat " + outsideFile}, nil) {
		t.Fatalf("out-of-domain command must NOT be auto-approved by in-domain bash session approval")
	}

	// 4. Approving out-of-domain command for session allows subsequent runs
	uiAllowOut := fakeUI{decision: tui.DecisionAllowSession}
	if !m.Check("Bash", map[string]any{"command": "cat " + outsideFile}, uiAllowOut) {
		t.Fatalf("expected out-of-domain command to succeed with AllowSession")
	}
	// Now running the command succeeds without UI prompt
	if !m.Check("Bash", map[string]any{"command": "cat " + outsideFile}, nil) {
		t.Fatalf("subsequent out-of-domain command should be allowed after AllowSession")
	}
}

func TestDomain_GlobAndGrep_Traversals(t *testing.T) {
	ws := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "target.txt")
	_ = os.WriteFile(outsideFile, []byte("needle in haystack"), 0o644)

	m := NewManager(&config.Config{Cwd: ws, YesMode: false})

	// 1. Glob with out-of-domain pattern
	if m.Check("Glob", map[string]any{"pattern": "../../other/*.go"}, nil) {
		t.Fatalf("Glob with ../../ pattern should require permission")
	}
	if m.Check("Glob", map[string]any{"pattern": "/etc/*.conf"}, nil) {
		t.Fatalf("Glob with /etc pattern should require permission")
	}
	if m.Check("Glob", map[string]any{"dir": outsideDir, "pattern": "*.txt"}, nil) {
		t.Fatalf("Glob with outside dir parameter should require permission")
	}

	// In-domain Glob without path remains safe
	if !m.Check("Glob", map[string]any{"pattern": "internal/**/*.go"}, nil) {
		t.Fatalf("in-domain Glob should be safe and allowed without UI")
	}

	// 2. Grep with out-of-domain file_path or glob
	if m.Check("Grep", map[string]any{"pattern": "needle", "file_path": outsideFile}, nil) {
		t.Fatalf("Grep with out-of-domain file_path should require permission")
	}
	if m.Check("Grep", map[string]any{"pattern": "needle", "dir": outsideDir}, nil) {
		t.Fatalf("Grep with out-of-domain dir should require permission")
	}
	if m.Check("Grep", map[string]any{"pattern": "needle", "glob": "../../other/*.go"}, nil) {
		t.Fatalf("Grep with ../../ glob should require permission")
	}

	// In-domain Grep remains safe
	if !m.Check("Grep", map[string]any{"pattern": "func", "path": ws}, nil) {
		t.Fatalf("in-domain Grep should be safe and allowed without UI")
	}
}

func TestDomain_GitDiff_OutDomain(t *testing.T) {
	ws := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.txt")
	inDomainFile := filepath.Join(ws, "local.txt")

	m := NewManager(&config.Config{Cwd: ws, YesMode: false})

	// In-domain GitDiff is safe
	if !m.Check("GitDiff", map[string]any{"file_path": inDomainFile}, nil) {
		t.Fatalf("in-domain GitDiff should be safe and allowed by default")
	}
	if !m.Check("GitDiff", map[string]any{}, nil) {
		t.Fatalf("GitDiff working tree should be safe and allowed by default")
	}

	// Out-of-domain GitDiff requires permission
	if m.Check("GitDiff", map[string]any{"file_path": outsideFile}, nil) {
		t.Fatalf("out-of-domain GitDiff should require permission and fail without UI")
	}

	// AllowOnce allows it
	ui := fakeUI{decision: tui.DecisionAllowOnce}
	if !m.Check("GitDiff", map[string]any{"file_path": outsideFile}, ui) {
		t.Fatalf("GitDiff should succeed with DecisionAllowOnce")
	}
}

func TestSystemTempPathAllowed(t *testing.T) {
	ws := "/home/user/project"
	m := NewManager(&config.Config{Cwd: ws})

	tempPaths := []string{
		"/tmp",
		"/tmp/test.txt",
		"/tmp/subdir/file.sh",
		"/var/tmp/download.zip",
	}

	for _, tp := range tempPaths {
		if !isAllowedSystemPath(tp, ws) {
			t.Errorf("expected %s to be allowed system path when workspace is %s", tp, ws)
		}
		unapproved := m.UnapprovedOutDomainPaths("Read", map[string]any{"file_path": tp})
		if len(unapproved) != 0 {
			t.Errorf("expected 0 unapproved paths for %s, got %v", tp, unapproved)
		}
	}
}

func TestSafeReadOnlyCommand(t *testing.T) {
	ws := "/home/user/project"

	safeCmds := []string{
		"ls",
		"ls -la",
		"ls -lh internal",
		"ls .",
		"ls ./cmd",
		"ls " + ws,
		"ls /tmp",
		"pwd",
		"dir",
	}

	for _, cmd := range safeCmds {
		if !IsSafeReadOnlyCommand(cmd, ws) {
			t.Errorf("expected %q to be safe read-only command", cmd)
		}
	}

	unsafeCmds := []string{
		"ls /etc",
		"ls > out.txt",
		"ls >> out.txt",
		"ls | rm -rf .",
		"ls; rm -rf .",
		"ls $(whoami)",
		"ls `whoami`",
		"rm file.txt",
		"cat file.txt",
		"go test ./...",
	}

	for _, cmd := range unsafeCmds {
		if IsSafeReadOnlyCommand(cmd, ws) {
			t.Errorf("expected %q NOT to be safe read-only command", cmd)
		}
	}
}

func TestManager_Check_AutoApprovesLSInWorkspace(t *testing.T) {
	ws := "/home/user/project"
	m := NewManager(&config.Config{Cwd: ws, YesMode: false})

	// 'ls' in workspace should be auto-approved without UI (returns true)
	if !m.Check("Bash", map[string]any{"command": "ls"}, nil) {
		t.Fatalf("expected 'ls' in workspace to be auto-approved without prompt")
	}

	if !m.Check("Bash", map[string]any{"command": "ls -la internal"}, nil) {
		t.Fatalf("expected 'ls -la internal' in workspace to be auto-approved without prompt")
	}

	// Mutating or unknown command must still require permission (returns false without UI)
	if m.Check("Bash", map[string]any{"command": "rm file.txt"}, nil) {
		t.Fatalf("expected 'rm file.txt' to NOT be auto-approved")
	}
}

