package background

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusKilled    Status = "killed"
)

const (
	maxOutputBuf     = 512 * 1024 // 512 KB
	outputTrimTarget = 256 * 1024 // 256 KB
)

// Task represents an asynchronous background terminal execution.
type Task struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	WorkDir   string    `json:"workdir,omitempty"`
	PID       int       `json:"pid"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time,omitempty"`
	Status    Status    `json:"status"`
	ExitCode  int       `json:"exit_code"`

	cmd       *exec.Cmd
	doneCh    chan struct{}
	outputBuf *bytes.Buffer
	readPos   int // byte offset up to which output has been read
	notified  bool
	killed    bool
	mu        sync.RWMutex
}

// IsRunning reports whether the task process is still active.
func (t *Task) IsRunning() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Status == StatusRunning
}

// Elapsed returns how long the task ran or has been running.
func (t *Task) Elapsed() time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.elapsedLocked()
}

func (t *Task) elapsedLocked() time.Duration {
	if !t.EndTime.IsZero() {
		return t.EndTime.Sub(t.StartTime)
	}
	return time.Since(t.StartTime)
}

// Output returns the full captured output up to maxOutputBuf.
func (t *Task) Output() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.outputBuf == nil {
		return ""
	}
	return t.outputBuf.String()
}

// NewOutput returns unread output since the last NewOutput call.
func (t *Task) NewOutput() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.outputBuf == nil {
		return ""
	}
	bufStr := t.outputBuf.String()
	if t.readPos > len(bufStr) {
		t.readPos = len(bufStr)
	}
	newOut := bufStr[t.readPos:]
	t.readPos = len(bufStr)
	return newOut
}

// TailOutput returns the last N lines of output.
func (t *Task) TailOutput(n int) string {
	out := t.Output()
	if n <= 0 || out == "" {
		return out
	}
	lines := strings.Split(out, "\n")
	if len(lines) <= n {
		return out
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// Snapshot returns a copy of task metadata safe for JSON serialization.
type TaskSnapshot struct {
	ID        string `json:"id"`
	Command   string `json:"command"`
	WorkDir   string `json:"workdir,omitempty"`
	PID       int    `json:"pid"`
	Status    Status `json:"status"`
	ExitCode  int    `json:"exit_code"`
	Elapsed   string `json:"elapsed"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

func (t *Task) Snapshot() TaskSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	elapsed := t.elapsedLocked()
	return TaskSnapshot{
		ID:        t.ID,
		Command:   t.Command,
		WorkDir:   t.WorkDir,
		PID:       t.PID,
		Status:    t.Status,
		ExitCode:  t.ExitCode,
		Elapsed:   elapsed.Round(100 * time.Millisecond).String(),
		ElapsedMS: elapsed.Milliseconds(),
	}
}

func (t *Task) appendOutput(data []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.outputBuf == nil {
		t.outputBuf = &bytes.Buffer{}
	}
	t.outputBuf.Write(data)
	if t.outputBuf.Len() > maxOutputBuf {
		oldLen := t.outputBuf.Len()
		dropped := oldLen - outputTrimTarget
		retained := t.outputBuf.Bytes()[dropped:]
		t.outputBuf.Reset()
		t.outputBuf.Write(retained)
		if t.readPos <= dropped {
			t.readPos = 0
		} else {
			t.readPos -= dropped
		}
	}
}

func (t *Task) markFinished(exitCode int, status Status) {
	t.mu.Lock()
	if t.Status != StatusRunning {
		t.mu.Unlock()
		return
	}
	if t.killed {
		status = StatusKilled
	}
	t.Status = status
	t.ExitCode = exitCode
	t.EndTime = time.Now()
	t.mu.Unlock()
	select {
	case <-t.doneCh:
	default:
		close(t.doneCh)
	}
}

func (t *Task) FormatStatusBanner() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	elapsed := t.elapsedLocked().Round(100 * time.Millisecond)
	switch t.Status {
	case StatusRunning:
		return fmt.Sprintf("[status: STILL RUNNING - execution in background (elapsed %s, pid %d)]", elapsed, t.PID)
	case StatusCompleted:
		return fmt.Sprintf("[status: COMPLETED (exit code %d, elapsed %s)]", t.ExitCode, elapsed)
	case StatusFailed:
		return fmt.Sprintf("[status: FAILED (exit code %d, elapsed %s)]", t.ExitCode, elapsed)
	case StatusKilled:
		return fmt.Sprintf("[status: KILLED (elapsed %s)]", elapsed)
	default:
		return fmt.Sprintf("[status: %s (elapsed %s)]", t.Status, elapsed)
	}
}
