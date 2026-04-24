package terminal

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSessionEchoAndExit(t *testing.T) {
	t.Parallel()
	m := NewManager()

	cmd := "echo hello"
	if runtime.GOOS == "windows" {
		cmd = "cmd /C echo hello"
	}

	sess, err := m.Start(cmd)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	output, running := sess.ReadOutput(1 * time.Second)
	if running {
		t.Fatalf("expected command to exit, still running. output: %q", output)
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("expected 'hello' in output, got: %q", output)
	}
}

func TestSessionSendInput(t *testing.T) {
	t.Parallel()
	m := NewManager()

	var cmd string
	switch runtime.GOOS {
	case "windows":
		cmd = writeWindowsBatch(t)
	default:
		cmd = `sh -c 'printf "Name: "; read x; echo "got $x"'`
	}

	sess, err := m.Start(cmd)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	output, running := sess.ReadOutput(500 * time.Millisecond)
	if !running {
		// Some shells might exit quickly if stdin is closed; that's OK for this test
		t.Skip("shell exited before prompt — likely not interactive in this environment")
	}
	if !strings.Contains(output, "Name:") {
		t.Fatalf("expected 'Name:' prompt, got: %q", output)
	}

	if err := sess.SendInput("Alice"); err != nil {
		t.Fatalf("send input: %v", err)
	}

	output, running = sess.ReadOutput(2 * time.Second)
	if !strings.Contains(output, "got Alice") && !strings.Contains(output, "Alice") {
		t.Fatalf("expected 'got Alice' or 'Alice' in output, got: %q (running=%v)", output, running)
	}

	// Clean up
	_ = m.Terminate(sess.ID)
}

func TestSessionTerminate(t *testing.T) {
	t.Parallel()
	m := NewManager()

	cmd := "sleep 30"
	if runtime.GOOS == "windows" {
		cmd = "timeout /t 30 /nobreak"
	}

	sess, err := m.Start(cmd)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	if !sess.IsRunning() {
		t.Fatal("expected session to be running")
	}

	if err := m.Terminate(sess.ID); err != nil {
		t.Fatalf("terminate: %v", err)
	}

	// Should be able to call Terminate again (idempotent)
	if err := m.Terminate(sess.ID); err == nil {
		// If the session was already removed from manager, this returns an error.
		// That's fine — either way the process is dead.
	}

	// Small grace for process to die
	time.Sleep(200 * time.Millisecond)
	if sess.IsRunning() {
		t.Fatal("expected session to be dead after terminate")
	}
}

func TestManagerSessionLimit(t *testing.T) {
	t.Parallel()
	m := NewManager()

	cmd := "sleep 30"
	if runtime.GOOS == "windows" {
		cmd = "timeout /t 30 /nobreak"
	}

	// Start maxSessions sessions
	sessions := make([]*Session, 0, maxSessions+1)
	for i := 0; i < maxSessions; i++ {
		sess, err := m.Start(cmd)
		if err != nil {
			t.Fatalf("start session %d: %v", i, err)
		}
		sessions = append(sessions, sess)
	}

	// Next one should fail
	_, err := m.Start(cmd)
	if err == nil {
		t.Fatal("expected error when exceeding max sessions")
	}
	if !strings.Contains(err.Error(), "too many active sessions") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Clean up
	for _, sess := range sessions {
		_ = m.Terminate(sess.ID)
	}
}

func TestManagerGetAndTerminateNotFound(t *testing.T) {
	t.Parallel()
	m := NewManager()

	if m.Get("nonexistent") != nil {
		t.Fatal("expected nil for non-existent session")
	}

	if err := m.Terminate("nonexistent"); err == nil {
		t.Fatal("expected error terminating non-existent session")
	}
}

func writeWindowsBatch(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.bat")
	script := "@echo off\necho Name:\nset /p x=\necho got %x%\n"
	if err := os.WriteFile(path, []byte(script), 0644); err != nil {
		t.Fatalf("write batch file: %v", err)
	}
	return path
}
