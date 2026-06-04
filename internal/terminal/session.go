package terminal

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/safety"
)

const (
	maxSessions     = 5
	sessionTTL      = 5 * time.Minute
	cleanupInterval = 30 * time.Second
)

// Manager holds interactive  sessions and handles lifecycle.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewManager creates a new session manager with background TTL cleanup.
func NewManager() *Manager {
	m := &Manager{
		sessions: make(map[string]*Session),
	}
	go m.cleanupLoop()
	return m
}

var defaultManager = NewManager()

// DefaultManager returns the shared global session manager.
func DefaultManager() *Manager {
	return defaultManager
}

// ResetDefaultManager creates a fresh default manager for testing.
func ResetDefaultManager() {
	defaultManager = NewManager()
}

// Start begins a new interactive session for the given command.
func (m *Manager) Start(command string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.sessions) >= maxSessions {
		m.cleanupUnlocked()
		if len(m.sessions) >= maxSessions {
			return nil, fmt.Errorf("too many active sessions (max %d)", maxSessions)
		}
	}

	id := generateSessionID()
	sess, err := newSession(id, command)
	if err != nil {
		return nil, err
	}

	m.sessions[id] = sess
	return sess, nil
}

// Get returns an existing session by ID.
func (m *Manager) Get(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

// Terminate kills a session by ID and removes it from the manager.
func (m *Manager) Terminate(id string) error {
	m.mu.Lock()
	sess, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	return sess.Terminate()
}

// TerminateAll kills every active session and clears the map.
func (m *Manager) TerminateAll() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()

	for _, s := range sessions {
		_ = s.Terminate()
	}
}

func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		m.cleanupUnlocked()
		m.mu.Unlock()
	}
}

func (m *Manager) cleanupUnlocked() {
	now := time.Now()
	for id, sess := range m.sessions {
		if now.Sub(sess.lastActivity) > sessionTTL {
			sess.Terminate()
			delete(m.sessions, id)
		}
	}
}

func generateSessionID() string {
	return fmt.Sprintf("term-%d", time.Now().UnixNano())
}

// Session represents a single interactive terminal session.
type Session struct {
	ID           string
	Command      string
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       io.ReadCloser
	stderr       io.ReadCloser
	outputBuf    *bytes.Buffer
	readPos      int // position up to which output has been consumed
	mu           sync.RWMutex
	exited       bool
	exitErr      error
	startTime    time.Time
	lastActivity time.Time
	doneCh       chan struct{}
	wg           sync.WaitGroup
}

func newSession(id, command string) (*Session, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	cmd.Env = safety.CleanEnv()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	sess := &Session{
		ID:           id,
		Command:      command,
		cmd:          cmd,
		stdin:        stdin,
		stdout:       stdout,
		stderr:       stderr,
		outputBuf:    &bytes.Buffer{},
		startTime:    time.Now(),
		lastActivity: time.Now(),
		doneCh:       make(chan struct{}),
	}

	// Start the process before launching the reader goroutines. If Start
	// fails we must not leak the copyOutput goroutines (and doneCh would
	// never close); close the pipes and bail out cleanly instead.
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start command: %w", err)
	}

	sess.wg.Add(2)
	go func() {
		defer sess.wg.Done()
		sess.copyOutput(stdout)
	}()
	go func() {
		defer sess.wg.Done()
		sess.copyOutput(stderr)
	}()

	go sess.waitForExit()
	return sess, nil
}

func (s *Session) copyOutput(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.outputBuf.Write(buf[:n])
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (s *Session) waitForExit() {
	err := s.cmd.Wait()
	s.wg.Wait()
	s.mu.Lock()
	s.exitErr = err
	s.exited = true
	s.mu.Unlock()
	close(s.doneCh)
}

// IsRunning returns true if the process is still running.
func (s *Session) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.exited
}

// SendInput writes a line of input to the session's stdin.
func (s *Session) SendInput(input string) error {
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()

	_, err := fmt.Fprintln(s.stdin, input)
	return err
}

// ReadOutput reads new output from the session with a timeout.
// It returns the newly produced output since the last call and a bool
// indicating whether the session is still running.
func (s *Session) ReadOutput(timeout time.Duration) (string, bool) {
	s.mu.Lock()
	readPos := s.readPos
	s.mu.Unlock()

	deadline := time.Now().Add(timeout)
	hasNewData := false

	for time.Now().Before(deadline) {
		s.mu.RLock()
		currentLen := s.outputBuf.Len()
		exited := s.exited
		s.mu.RUnlock()

		if currentLen > readPos {
			hasNewData = true
			readPos = currentLen
			// Keep reading briefly to catch rapid output
			deadline = time.Now().Add(200 * time.Millisecond)
			time.Sleep(50 * time.Millisecond)
			continue
		}

		if hasNewData && (exited || time.Now().After(deadline)) {
			break
		}
		if exited {
			break
		}

		time.Sleep(50 * time.Millisecond)
	}

	s.mu.Lock()
	result := s.outputBuf.String()[s.readPos:readPos]
	s.readPos = readPos
	stillRunning := !s.exited
	s.mu.Unlock()

	return result, stillRunning
}

// FinalOutput returns all unconsumed output.
func (s *Session) FinalOutput() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outputBuf.String()[s.readPos:]
}

// Terminate kills the session's process gracefully (interrupt on Unix, kill on
// Windows) and waits for it to exit.
func (s *Session) Terminate() error {
	s.mu.Lock()
	if s.exited {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	if s.cmd.Process != nil {
		if runtime.GOOS == "windows" {
			_ = s.cmd.Process.Kill()
		} else {
			_ = s.cmd.Process.Signal(os.Interrupt)
			time.Sleep(100 * time.Millisecond)
			s.mu.RLock()
			exited := s.exited
			s.mu.RUnlock()
			if !exited {
				_ = s.cmd.Process.Kill()
			}
		}
	}

	<-s.doneCh
	return nil
}

// StartTime returns when the session was created.
func (s *Session) StartTime() time.Time {
	return s.startTime
}
