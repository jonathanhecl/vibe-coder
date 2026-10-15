package lsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// defaultHandshakeTimeout bounds one initialize round trip. It is generous
// because a cold server may have to start an index, but it is still a bound: a
// server that accepts the connection and never answers must not hold a turn.
const defaultHandshakeTimeout = 20 * time.Second

var (
	// ErrNoServer means nothing could answer for this file: an extension no
	// family owns, a workspace without the right marker, or a server binary that
	// is not on PATH. Callers treat it as "no diagnostics", not as a failure.
	ErrNoServer = errors.New("lsp: no language server available")
	// ErrClosed means the manager was shut down.
	ErrClosed = errors.New("lsp: manager is closed")
)

// Config configures a Manager.
type Config struct {
	// Root is the workspace root. Language markers are probed there, it becomes
	// the rootUri of every handshake, and relative file paths resolve against
	// it.
	Root string
	// Servers overrides the command a family runs, already split into argv.
	// An override needs no workspace marker: naming a server explicitly is
	// reason enough to try it.
	Servers map[string][]string
	// Timeout bounds the wait for the diagnostics publication that answers one
	// edit. Zero means DefaultDiagnosticsTimeout.
	Timeout time.Duration
	// HandshakeTimeout bounds the initialize round trip. Zero means
	// defaultHandshakeTimeout.
	HandshakeTimeout time.Duration
}

// Result is the answer to one diagnostics query.
type Result struct {
	// Language is the family that answered.
	Language string
	// Diagnostics is what the server last reported for the file.
	Diagnostics []Diagnostic
	// Fresh is true when that list belongs to the version of the file this
	// query sent. It is false when the wait timed out or was cancelled, and the
	// list is then whatever the server said earlier, possibly nothing. Callers
	// must not read a stale list as a clean bill of health.
	Fresh bool
}

// Manager owns the language servers of one workspace: which families are usable
// here, and the single process per family that serves every edit and turn.
type Manager struct {
	cfg Config

	mu       sync.Mutex
	resolved map[string][]string // family → command, positives only
	sessions map[string]*sessionEntry
	closed   bool
}

// sessionEntry is a session in the map while it is starting, and the cache once
// it has started: a caller that finds an entry waits on done instead of spawning
// a second server for the same language.
type sessionEntry struct {
	done chan struct{}
	sess *session
	err  error
}

// NewManager prepares a manager for a workspace. No server is started until a
// file needs one.
func NewManager(cfg Config) *Manager {
	if abs, err := filepath.Abs(filepath.Clean(cfg.Root)); err == nil {
		cfg.Root = abs
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultDiagnosticsTimeout
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = defaultHandshakeTimeout
	}
	return &Manager{
		cfg:      cfg,
		resolved: make(map[string][]string),
		sessions: make(map[string]*sessionEntry),
	}
}

// Resolved lists the families usable in this workspace and the command each
// would run, keyed by family: the state /status reports.
func (m *Manager) Resolved() map[string]string {
	out := make(map[string]string)
	for _, spec := range defaultServers {
		if command, ok := m.resolve(spec.language); ok {
			out[spec.language] = strings.Join(command, " ")
		}
	}
	return out
}

// Available reports whether this workspace has at least one usable language
// server, which is what decides whether the feature turns itself on.
func (m *Manager) Available() bool {
	return len(m.Resolved()) > 0
}

// Supports reports whether diagnostics can be produced for a file right now.
func (m *Manager) Supports(filePath string) bool {
	language, ok := Language(filePath)
	if !ok {
		return false
	}
	_, ok = m.resolve(language)
	return ok
}

// Diagnostics syncs a file to its language server and returns what the server
// reports about it. An unavailable server is ErrNoServer, never a failure of the
// session, and a server that broke is reported with its stderr so the debug log
// can explain it.
func (m *Manager) Diagnostics(ctx context.Context, filePath string) (Result, error) {
	language, ok := Language(filePath)
	if !ok {
		return Result{}, fmt.Errorf("%w for %s", ErrNoServer, strings.TrimSpace(filePath))
	}
	command, ok := m.resolve(language)
	if !ok {
		return Result{}, fmt.Errorf("%w for %s files", ErrNoServer, language)
	}
	path := m.abs(filePath)
	text, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	entry, err := m.sessionFor(ctx, language, command)
	if err != nil {
		return Result{}, err
	}
	diagnostics, fresh, err := entry.sess.diagnostics(ctx, path, string(text))
	if err != nil {
		if entry.sess.dead() {
			// The server is gone. Dropping it means the next edit starts a fresh
			// one instead of paying a failing call on every turn.
			m.forget(language, entry)
			entry.sess.close()
		}
		return Result{}, err
	}
	return Result{Language: language, Diagnostics: diagnostics, Fresh: fresh}, nil
}

// Close shuts every language server down and stops accepting work.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	entries := make([]*sessionEntry, 0, len(m.sessions))
	for _, entry := range m.sessions {
		entries = append(entries, entry)
	}
	m.sessions = make(map[string]*sessionEntry)
	m.mu.Unlock()

	for _, entry := range entries {
		select {
		case <-entry.done:
		case <-time.After(m.cfg.HandshakeTimeout):
			// Still handshaking. Its starter sees the closed manager and tears
			// this session down itself, so nothing is orphaned either way.
			continue
		}
		if entry.sess != nil {
			entry.sess.close()
		}
	}
}

// resolve returns the command for a family, probing at most once per positive
// answer. A negative answer is re-probed so installing a server mid-session
// takes effect, mirroring AutoTest.detectCached: a marker rarely appears in a
// fixed workspace, but a PATH can change.
func (m *Manager) resolve(language string) ([]string, bool) {
	m.mu.Lock()
	cached, ok := m.resolved[language]
	m.mu.Unlock()
	if ok {
		return cached, true
	}

	command, ok := m.probe(language)
	if !ok {
		return nil, false
	}

	m.mu.Lock()
	if existing, ok := m.resolved[language]; ok {
		command = existing
	} else {
		m.resolved[language] = command
	}
	m.mu.Unlock()
	return command, true
}

// probe decides whether a family can run here: an explicit override wins, and
// otherwise the workspace has to carry one of the family's markers and one of
// its commands has to be on PATH.
func (m *Manager) probe(language string) ([]string, bool) {
	if command, ok := m.cfg.Servers[language]; ok && len(command) > 0 {
		if _, err := exec.LookPath(command[0]); err == nil {
			return command, true
		}
		return nil, false
	}
	spec, ok := specFor(language)
	if !ok {
		return nil, false
	}
	if !m.hasMarker(spec.markers) {
		return nil, false
	}
	for _, candidate := range spec.commands {
		if _, err := exec.LookPath(candidate[0]); err == nil {
			return candidate, true
		}
	}
	return nil, false
}

// hasMarker reports whether the workspace carries one of the given markers.
func (m *Manager) hasMarker(markers []string) bool {
	for _, marker := range markers {
		if _, err := os.Stat(filepath.Join(m.cfg.Root, marker)); err == nil {
			return true
		}
	}
	return false
}

// abs resolves a tool-supplied path against the workspace root, which is what a
// relative path from the model is relative to.
func (m *Manager) abs(filePath string) string {
	path := strings.TrimSpace(filePath)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(m.cfg.Root, path)
}

// sessionFor returns the session entry for a family, starting the server on
// first need.
//
// A failed start is not cached: a server that could not run once is retried on
// the next edit rather than disabling the feature for the rest of the session.
// sessionFor returns the session for a language, starting one on first need.
//
// The returned entry doubles as the single-flight marker: a caller that finds a
// start already in flight waits for it instead of spawning a second server for
// the same language.
func (m *Manager) sessionFor(ctx context.Context, language string, command []string) (*sessionEntry, error) {
	// A cancelled call is answered before anything else happens, so it is
	// deterministic and so it never starts a server nothing is waiting for.
	// Checking it explicitly matters because a select over a ready entry and a
	// done context picks between them at random.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	if entry, ok := m.sessions[language]; ok {
		m.mu.Unlock()
		select {
		case <-entry.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if entry.err != nil {
			m.forget(language, entry)
			return nil, entry.err
		}
		return entry, nil
	}
	entry := &sessionEntry{done: make(chan struct{})}
	m.sessions[language] = entry
	m.mu.Unlock()

	sess, err := startSession(ctx, language, command, m.cfg)
	entry.sess, entry.err = sess, err
	close(entry.done)
	if err != nil {
		m.forget(language, entry)
		return nil, err
	}

	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		// Close ran during the handshake and no longer tracks this session, so
		// it has to be torn down here or the server outlives the agent.
		m.forget(language, entry)
		sess.close()
		return nil, ErrClosed
	}
	return entry, nil
}

// forget drops a session entry the manager no longer wants cached.
func (m *Manager) forget(language string, entry *sessionEntry) {
	m.mu.Lock()
	if current, ok := m.sessions[language]; ok && current == entry {
		delete(m.sessions, language)
	}
	m.mu.Unlock()
}
