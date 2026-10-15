package lsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/safety"
)

const (
	// shutdownTimeout bounds the shutdown round trip. It is short because the
	// answer only has to be read, not acted on: whatever happens next, the
	// process is killed after exitGrace.
	shutdownTimeout = 2 * time.Second
	// exitGrace is how long a server gets to leave on its own after the exit
	// notification before it is killed.
	exitGrace = 5 * time.Second
	// killWait bounds the reap of a killed process.
	killWait = 2 * time.Second
	// stderrTailBytes is how much of a server's stderr is kept. What a server
	// prints right before it dies is the only evidence of why it died, and
	// unbounded capture would hold a whole indexing log in memory.
	stderrTailBytes = 4096
	// stderrDetailBytes caps the stderr excerpt carried in an error message.
	stderrDetailBytes = 512
	// maxOpenDocuments bounds how many documents stay open in a server. A server
	// keeps a parse of every document it was told about, and a session touches
	// far more files than one task needs; the least recently edited ones are the
	// ones to let go, because those are the files an agent comes back to.
	maxOpenDocuments = 32
)

// session is one running language server plus the documents synced to it.
//
// One mutex serializes every conversation with the server. A document sync and
// the wait for the publication that answers it are a single exchange: a second
// didChange slipping between them would make the version this caller waits on
// meaningless.
type session struct {
	language string
	command  []string
	timeout  time.Duration

	cmd    *exec.Cmd
	client *Client
	stderr *tailBuffer
	exited chan struct{}

	mu   sync.Mutex
	docs map[string]int // document URI → last version sent
	open []string       // open document URIs, least recently edited first
}

// startSession spawns a language server and completes the handshake.
//
// The process is deliberately not exec.CommandContext: it is shared by every
// later edit and turn, so killing it because one call's context expired would
// drop a server other calls are still using. Only close owns its lifetime, and a
// failure on the way in kills it before returning so a half-started server is
// never left behind.
func startSession(ctx context.Context, language string, command []string, cfg Config) (*session, error) {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = cfg.Root
	cmd.Env = safety.CleanEnv()

	sess := &session{
		language: language,
		command:  command,
		timeout:  cfg.Timeout,
		cmd:      cmd,
		stderr:   newTailBuffer(stderrTailBytes),
		exited:   make(chan struct{}),
		docs:     make(map[string]int),
	}
	// Stderr goes to a tail buffer through exec's own copying goroutine, which
	// Wait drains to completion.
	cmd.Stderr = sess.stderr

	pipes, err := newProcessPipes(cmd)
	if err != nil {
		return nil, fmt.Errorf("lsp: %s: %w", language, err)
	}
	if err := cmd.Start(); err != nil {
		pipes.closeParent()
		return nil, fmt.Errorf("lsp: %s: start %s: %w%s", language, strings.Join(command, " "), err, sess.stderrDetail())
	}
	// The child holds its own copies of its ends; closing the parent's copies is
	// what makes a reader see EOF once the child is gone.
	pipes.closeChildEnds()
	go func() {
		_ = cmd.Wait()
		close(sess.exited)
	}()

	sess.client = New(&processConn{reader: pipes.fromServer, writer: pipes.toServer})

	handshake, cancel := context.WithTimeout(ctx, cfg.HandshakeTimeout)
	defer cancel()
	if _, err := sess.client.Initialize(handshake, fileURI(cfg.Root)); err != nil {
		detail := sess.stderrDetail()
		// A server that never answered has no conversation to end politely, and
		// the graceful path would spend the shutdown timeout and the exit grace
		// on it: seconds taken straight out of a turn.
		sess.abort()
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("lsp: %s: initialize did not answer within %s%s", language, cfg.HandshakeTimeout, detail)
		}
		return nil, fmt.Errorf("lsp: %s: initialize: %w%s", language, err, detail)
	}
	return sess, nil
}

// dead reports a transport that has ended, which makes every later call on this
// session fail fast instead of waiting for a server that is gone.
func (s *session) dead() bool {
	return s.client != nil && s.client.Err() != nil
}

// diagnostics syncs the document and waits for the publication that carries the
// version just sent.
//
// Fresh is false when nothing arrived for that version, which includes a
// timeout. The caller gets what the server last said, so a stale list can never
// be presented as the answer to the edit that was just made.
func (s *session) diagnostics(ctx context.Context, path, text string) ([]Diagnostic, bool, error) {
	if err := s.client.Err(); err != nil {
		return nil, false, fmt.Errorf("lsp: %s: %w", s.language, err)
	}
	uri := fileURI(path)

	s.mu.Lock()
	defer s.mu.Unlock()

	// The first sync opens the document, every later one replaces its content
	// wholesale (a range-less change is valid under every sync mode, so the
	// client never has to track edits).
	version := s.docs[uri] + 1
	var err error
	if version == 1 {
		err = s.client.DidOpen(uri, documentLanguageID(path), version, text)
	} else {
		err = s.client.DidChange(uri, version, text)
	}
	if err != nil {
		return nil, false, fmt.Errorf("lsp: %s: %w", s.language, err)
	}
	s.docs[uri] = version
	// The window of documents kept open is bounded: one that falls out of it is
	// reopened from scratch on the next edit, which costs a parse and keeps a
	// long session from making the server hold every file it ever touched.
	s.touch(uri)
	s.evict()

	diagnostics, fresh := s.client.WaitDiagnostics(ctx, uri, version, s.timeout)
	return diagnostics, fresh, nil
}

// touch marks a document as the most recently edited one. The window is a few
// dozen entries at most, so a linear scan is cheaper than a list type.
//
// The caller holds s.mu.
func (s *session) touch(uri string) {
	for i, open := range s.open {
		if open != uri {
			continue
		}
		copy(s.open[i:], s.open[i+1:])
		s.open[len(s.open)-1] = uri
		return
	}
	s.open = append(s.open, uri)
}

// evict closes the documents past the open window, oldest first. A notification
// that fails is not actionable: the document is forgotten here either way, and
// the next edit to it opens it again.
//
// The caller holds s.mu.
func (s *session) evict() {
	for len(s.open) > maxOpenDocuments {
		oldest := s.open[0]
		s.open = s.open[1:]
		delete(s.docs, oldest)
		if s.client == nil {
			continue
		}
		_ = s.client.DidClose(oldest)
	}
}

// clearOpen forgets every open document and hands back its URI, so a caller can
// release them while the server is still there to be told.
func (s *session) clearOpen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	open := s.open
	s.open = nil
	s.docs = make(map[string]int)
	return open
}

// close tears the server down: shutdown, exit, a bounded wait for it to leave on
// its own, then a kill. It is safe to call more than once.
func (s *session) close() {
	if s.client != nil {
		// Documents are released before the shutdown: a server told about a
		// closed document can drop the parse and the watchers it kept for it,
		// and the notification only lands while the process is still reading.
		for _, uri := range s.clearOpen() {
			_ = s.client.DidClose(uri)
		}

		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		_ = s.client.Call(ctx, "shutdown", nil, nil)
		cancel()
		_ = s.client.Notify("exit", nil)
	}

	if !s.waitExit(exitGrace) {
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		_ = s.waitExit(killWait)
	}
	// Closing the client ends the read loop and closes the transport, which
	// releases a reader blocked on a server that outlived the kill.
	if s.client != nil {
		_ = s.client.Close()
	}
}

// abort ends a session whose handshake never completed. The server is killed
// outright instead of being asked to shut down, because there is no conversation
// to end and a server that has never answered would hold the turn for the whole
// graceful teardown. Safe to call after close.
func (s *session) abort() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.waitExit(killWait)
	// Closing the client ends the read loop and closes the transport, which
	// releases a reader blocked on a process that is already gone.
	if s.client != nil {
		_ = s.client.Close()
	}
}

// waitExit reports whether the process ended within the timeout.
func (s *session) waitExit(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-s.exited:
		return true
	case <-timer.C:
		return false
	}
}

// stderrDetail renders what the server printed as an error suffix, collapsed to
// one line. It is what makes a broken server diagnosable from the debug log
// without adding logging to this package.
func (s *session) stderrDetail() string {
	tail := strings.TrimSpace(s.stderr.String())
	if tail == "" {
		return ""
	}
	tail = strings.Join(strings.Fields(tail), " ")
	if len(tail) > stderrDetailBytes {
		tail = tail[:stderrDetailBytes] + "..."
	}
	return " (stderr: " + tail + ")"
}

// processPipes holds the parent's ends of a child's stdio.
type processPipes struct {
	toServer   *os.File // parent writes here; the server reads it as stdin
	fromServer *os.File // parent reads here; the server writes it as stdout
	childEnds  []*os.File
}

// newProcessPipes wires a command's stdin and stdout to pipes owned by the
// parent instead of using cmd.StdinPipe and cmd.StdoutPipe.
//
// The difference matters for a long-lived child: Wait closes the pipes created
// by those helpers, which races with a reader still draining them, and this
// client reads the server's stdout for as long as the session lives. With plain
// os.Pipe ends, Wait only reaps the process and the reader is closed by the
// transport.
func newProcessPipes(cmd *exec.Cmd) (*processPipes, error) {
	toServerR, toServerW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	fromServerR, fromServerW, err := os.Pipe()
	if err != nil {
		_ = toServerR.Close()
		_ = toServerW.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stdin = toServerR
	cmd.Stdout = fromServerW
	return &processPipes{
		toServer:   toServerW,
		fromServer: fromServerR,
		childEnds:  []*os.File{toServerR, fromServerW},
	}, nil
}

// closeChildEnds drops the parent's copies of the child's ends. The child keeps
// its own, inherited across the fork.
func (p *processPipes) closeChildEnds() {
	for _, file := range p.childEnds {
		_ = file.Close()
	}
}

// closeParent drops the parent's ends, for the paths that never reached Start.
func (p *processPipes) closeParent() {
	_ = p.toServer.Close()
	_ = p.fromServer.Close()
	p.closeChildEnds()
}

// processConn presents a language server's two unidirectional pipes as the
// single byte stream the protocol client speaks.
type processConn struct {
	reader *os.File
	writer *os.File
}

func (c *processConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (c *processConn) Write(p []byte) (int, error) { return c.writer.Write(p) }

func (c *processConn) Close() error {
	err := c.reader.Close()
	if writeErr := c.writer.Close(); err == nil {
		err = writeErr
	}
	return err
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu   sync.Mutex
	max  int
	data []byte
}

func newTailBuffer(max int) *tailBuffer {
	return &tailBuffer{max: max}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.max {
		b.data = append([]byte(nil), b.data[len(b.data)-b.max:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
