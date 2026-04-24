package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutoTestDetectGo(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module x\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	a := NewAutoTest(tmp)
	if !a.Enabled() {
		t.Fatalf("expected autotest enabled for go.mod")
	}
	out := a.RunAfterEdit(context.Background(), "x.go")
	if out != "" {
		// may fail if no package; just ensure format when failing
		if !strings.Contains(out, "[AUTO-TEST]") {
			t.Fatalf("unexpected autotest output: %q", out)
		}
	}
}

func TestShouldRunAutoTestForFile(t *testing.T) {
	tests := []struct {
		name     string
		testName string
		filePath string
		want     bool
	}{
		{name: "pytest with python file", testName: "pytest", filePath: "src/main.py", want: true},
		{name: "pytest with batch file", testName: "pytest", filePath: "run.bat", want: false},
		{name: "go with go file", testName: "go", filePath: "internal/app.go", want: true},
		{name: "go with markdown file", testName: "go", filePath: "README.md", want: false},
		{name: "unknown test keeps default", testName: "other", filePath: "any.txt", want: true},
		{name: "empty path keeps backward compatibility", testName: "pytest", filePath: "", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRunAutoTestForFile(tt.testName, tt.filePath)
			if got != tt.want {
				t.Fatalf("shouldRunAutoTestForFile(%q, %q) = %t, want %t", tt.testName, tt.filePath, got, tt.want)
			}
		})
	}
}

func TestCheckpointNoRepo(t *testing.T) {
	tmp := t.TempDir()
	c := NewCheckpoint(tmp)
	if c.IsRepo() {
		t.Fatalf("expected non-repo temp dir")
	}
	if err := c.Create("x"); err != nil {
		t.Fatalf("checkpoint create on non-repo should be no-op: %v", err)
	}
}

func TestCheckpointCreateInRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	run := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmp
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmtError(string(out), err)
		}
		return nil
	}
	if err := run("init"); err != nil {
		t.Skipf("unable to init git repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	c := NewCheckpoint(tmp)
	if !c.IsRepo() {
		t.Fatalf("expected repo")
	}
	_ = c.Create("test")
}

func fmtError(out string, err error) error {
	return &gitErr{out: out, err: err}
}

type gitErr struct {
	out string
	err error
}

func (e *gitErr) Error() string {
	return strings.TrimSpace(e.out) + ": " + e.err.Error()
}
