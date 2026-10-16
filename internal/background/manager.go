package background

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/safety"
)

type Manager struct {
	mu        sync.RWMutex
	tasks     map[string]*Task
	taskSeq   uint64
	listeners []func(task *Task)
}

func NewManager() *Manager {
	return &Manager{
		tasks: make(map[string]*Task),
	}
}

var defaultManager = NewManager()

func DefaultManager() *Manager {
	return defaultManager
}

func ResetDefaultManager() {
	defaultManager = NewManager()
}

// OnTaskFinished registers a listener called when any background task terminates.
func (m *Manager) OnTaskFinished(fn func(task *Task)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listeners = append(m.listeners, fn)
}

func (m *Manager) notifyListeners(task *Task) {
	m.mu.RLock()
	list := make([]func(task *Task), len(m.listeners))
	copy(list, m.listeners)
	m.mu.RUnlock()

	for _, fn := range list {
		if fn != nil {
			go fn(task)
		}
	}
}

// Start begins a background task. It waits up to initialWait before returning:
// if the command completes quickly within initialWait, it returns the finished task;
// otherwise, it returns the running task so the caller knows it is executing in the background.
func (m *Manager) Start(command, workdir string, initialWait time.Duration) (*Task, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}

	if blocked, reason := safety.IsDangerousCommand(command); blocked {
		return nil, fmt.Errorf("dangerous command blocked: %s", reason)
	}
	if safety.IsProtectedWrite(command) {
		return nil, fmt.Errorf("protected config writes are blocked")
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	cmd.Env = safety.CleanEnv()
	if strings.TrimSpace(workdir) != "" {
		cmd.Dir = workdir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	seq := atomic.AddUint64(&m.taskSeq, 1)
	id := fmt.Sprintf("bg-%d", seq)

	task := &Task{
		ID:        id,
		Command:   command,
		WorkDir:   workdir,
		StartTime: time.Now(),
		Status:    StatusRunning,
		ExitCode:  -1,
		cmd:       cmd,
		doneCh:    make(chan struct{}),
	}

	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start command: %w", err)
	}

	if cmd.Process != nil {
		task.PID = cmd.Process.Pid
	}

	m.mu.Lock()
	m.tasks[id] = task
	m.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(2)
	go m.copyStream(task, stdout, &wg)
	go m.copyStream(task, stderr, &wg)

	go func() {
		drained := make(chan struct{})
		go func() {
			wg.Wait()
			close(drained)
		}()
		select {
		case <-drained:
		case <-time.After(1 * time.Second):
		}

		err := cmd.Wait()
		wg.Wait()

		exitCode := 0
		status := StatusCompleted
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
			status = StatusFailed
		}
		task.markFinished(exitCode, status)
		m.notifyListeners(task)
	}()

	if initialWait > 0 {
		select {
		case <-task.doneCh:
			// Finished during initial wait
		case <-time.After(initialWait):
			// Still running
		}
	}

	return task, nil
}

func (m *Manager) copyStream(task *Task, r io.ReadCloser, wg *sync.WaitGroup) {
	defer wg.Done()
	defer r.Close()

	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			task.appendOutput(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// Get returns the task by ID, or nil if not found.
func (m *Manager) Get(id string) *Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tasks[id]
}

// List returns a snapshot list of all known background tasks, newest first.
func (m *Manager) List() []*Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartTime.After(out[j].StartTime)
	})
	return out
}

// Wait waits up to timeout for the given task to finish. If timeout <= 0,
// it returns the task's current status immediately without blocking.
// Returns task, finished (bool), and error.
func (m *Manager) Wait(ctx context.Context, id string, timeout time.Duration) (*Task, bool, error) {
	task := m.Get(id)
	if task == nil {
		return nil, false, fmt.Errorf("background task not found: %s", id)
	}

	if !task.IsRunning() {
		return task, true, nil
	}

	if timeout <= 0 {
		return task, false, nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return task, !task.IsRunning(), ctx.Err()
	case <-task.doneCh:
		return task, true, nil
	case <-timer.C:
		return task, !task.IsRunning(), nil
	}
}

// Kill terminates the background task.
func (m *Manager) Kill(id string) error {
	task := m.Get(id)
	if task == nil {
		return fmt.Errorf("background task not found: %s", id)
	}

	task.mu.Lock()
	if task.Status != StatusRunning {
		task.mu.Unlock()
		return nil
	}
	task.killed = true
	cmd := task.cmd
	task.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		if runtime.GOOS == "windows" {
			_ = cmd.Process.Kill()
		} else {
			_ = cmd.Process.Signal(os.Interrupt)
			time.Sleep(100 * time.Millisecond)
			if task.IsRunning() {
				_ = cmd.Process.Kill()
			}
		}
	}

	select {
	case <-task.doneCh:
	case <-time.After(1 * time.Second):
		task.markFinished(-1, StatusKilled)
	}

	return nil
}

// KillAll terminates all currently running background tasks.
func (m *Manager) KillAll() {
	for _, t := range m.List() {
		if t.IsRunning() {
			_ = m.Kill(t.ID)
		}
	}
}

// DrainUnnotifiedCompleted returns all tasks that finished since the last call,
// marking them as notified so they are only reported once to the agent/model.
func (m *Manager) DrainUnnotifiedCompleted() []*Task {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []*Task
	for _, t := range m.tasks {
		t.mu.Lock()
		if (t.Status == StatusCompleted || t.Status == StatusFailed || t.Status == StatusKilled) && !t.notified {
			t.notified = true
			out = append(out, t)
		}
		t.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].EndTime.Before(out[j].EndTime)
	})
	return out
}
