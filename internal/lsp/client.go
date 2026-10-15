// Package lsp speaks the slice of the Language Server Protocol that answers the
// one question the harness asks after an edit: what is wrong with this file?
// It is a transport-level client over any byte stream — an exec.Cmd pipe in
// production, an in-memory pipe in tests — and knows nothing about processes,
// workspaces or paths, which are the caller's business.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"
)

var (
	// ErrClientClosed is returned by any call on a client whose transport has
	// ended, so a caller can tell a dead server from a slow one.
	ErrClientClosed = errors.New("lsp: client closed")

	// errServerClosed marks the normal end of the stream: a server that exits
	// has not failed, it has finished.
	errServerClosed = errors.New("lsp: server closed the stream")

	// closeGrace bounds how long Close waits for the read loop to notice a
	// closed transport. Closing the transport is what unblocks the loop, so
	// this is a leak guard rather than the expected path.
	closeGrace = 2 * time.Second
)

// NotificationHandler receives a server notification's params.
type NotificationHandler func(params json.RawMessage)

// RequestHandler answers a request the server sends to the client. Returning an
// error sends a JSON-RPC error back, because a server left waiting on a request
// is a server that has stopped publishing.
type RequestHandler func(params json.RawMessage) (any, error)

type rpcResult struct {
	result json.RawMessage
	err    error
}

// Client speaks LSP over a byte stream. The read loop starts with the client,
// so a transport handed to New is never left unread, and New is the only
// constructor a caller needs.
type Client struct {
	conn io.ReadWriteCloser

	// writeMu serializes whole frames. Two requests written concurrently must
	// never interleave their headers and payloads on the wire.
	writeMu sync.Mutex

	mu        sync.Mutex
	nextID    int64
	pending   map[int64]chan rpcResult
	notifiers map[string][]NotificationHandler
	requests  map[string]RequestHandler
	closed    bool
	transport error
	done      chan struct{}
	store     *diagnosticsStore
}

// New starts a client over conn. The connection is owned by the client: Close
// closes it.
func New(conn io.ReadWriteCloser) *Client {
	c := &Client{
		conn:      conn,
		nextID:    1,
		pending:   map[int64]chan rpcResult{},
		notifiers: map[string][]NotificationHandler{},
		requests:  map[string]RequestHandler{},
		done:      make(chan struct{}),
		store:     newDiagnosticsStore(),
	}
	// The one notification the client never delegates: diagnostics are what
	// this package exists for, so they are wired before any caller can
	// subscribe and cannot be missed by forgetting to.
	c.OnNotification("textDocument/publishDiagnostics", c.store.handlePublish)
	go c.readLoop()
	return c
}

// OnNotification registers a handler for a server notification. Handlers run on
// the read loop, so they must not block on anything but the store.
func (c *Client) OnNotification(method string, fn NotificationHandler) {
	if fn == nil {
		return
	}
	c.mu.Lock()
	c.notifiers[method] = append(c.notifiers[method], fn)
	c.mu.Unlock()
}

// RegisterRequest teaches the client how to answer a server request. Every
// other request is answered with method-not-found, which is deliberate: gopls
// asks workspace/configuration while starting and holds its diagnostics until
// that request is settled.
func (c *Client) RegisterRequest(method string, handler RequestHandler) {
	c.mu.Lock()
	if handler == nil {
		delete(c.requests, method)
	} else {
		c.requests[method] = handler
	}
	c.mu.Unlock()
}

// Err returns the transport error that ended the client, if any. It exists for
// the debug log: a broken language server never reaches the model.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.transport
}

// Call sends a request and decodes its result into result, which may be nil
// when the caller does not care. The context bounds the wait; the request is
// forgotten either way.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClientClosed
	}
	id := c.nextID
	c.nextID++
	// Buffered so a response arriving after the caller gave up is dropped
	// instead of blocking the read loop that delivers every other message.
	ch := make(chan rpcResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.write(outgoingMessage{JSONRPC: protocolVersion, ID: id, Method: method, Params: params}); err != nil {
		c.forget(id)
		return err
	}

	select {
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	case <-c.done:
		c.forget(id)
		return ErrClientClosed
	case res := <-ch:
		if res.err != nil {
			return fmt.Errorf("lsp %s failed: %w", method, res.err)
		}
		if result == nil || len(res.result) == 0 {
			return nil
		}
		if err := json.Unmarshal(res.result, result); err != nil {
			return fmt.Errorf("lsp: decode %s result: %w", method, err)
		}
		return nil
	}
}

// Notify sends a notification: no answer is expected and none is waited for.
func (c *Client) Notify(method string, params any) error {
	return c.write(outgoingMessage{JSONRPC: protocolVersion, Method: method, Params: params})
}

// Close shuts the client down. It is safe to call more than once.
func (c *Client) Close() error {
	c.mu.Lock()
	alreadyClosed := c.closed
	c.closed = true
	c.mu.Unlock()

	select {
	case <-c.done:
		return nil
	default:
	}

	if alreadyClosed {
		c.awaitReadLoop()
		return nil
	}
	err := c.conn.Close()
	c.fail(ErrClientClosed)
	c.awaitReadLoop()
	if err != nil {
		return fmt.Errorf("lsp: close transport: %w", err)
	}
	return nil
}

func (c *Client) awaitReadLoop() {
	select {
	case <-c.done:
	case <-time.After(closeGrace):
	}
}

func (c *Client) readLoop() {
	defer close(c.done)
	reader := bufio.NewReader(c.conn)
	for {
		payload, err := ReadMessage(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.fail(errServerClosed)
				return
			}
			c.fail(err)
			return
		}
		var msg incomingMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			// One malformed message is the server's problem, not the
			// session's: drop it and keep reading, so a single bad frame
			// cannot cost the run every diagnostic after it.
			continue
		}
		c.dispatch(msg)
	}
}

func (c *Client) dispatch(msg incomingMessage) {
	switch {
	case msg.Method != "" && msg.ID != nil:
		c.handleServerRequest(msg)
	case msg.Method != "":
		c.handleNotification(msg)
	case msg.ID != nil:
		c.handleResponse(msg)
	}
}

func (c *Client) handleResponse(msg incomingMessage) {
	id, ok := numericID(msg.ID)
	if !ok {
		// A string ID is not an answer to a request this client sent.
		return
	}
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- rpcResult{result: msg.Result, err: errorFrom(msg.Error)}:
	default:
	}
}

func (c *Client) handleNotification(msg incomingMessage) {
	c.mu.Lock()
	// Handlers run outside the lock: one that registers another handler would
	// otherwise deadlock the read loop that delivers every message.
	handlers := slices.Clone(c.notifiers[msg.Method])
	c.mu.Unlock()
	for _, fn := range handlers {
		fn(msg.Params)
	}
}

func (c *Client) handleServerRequest(msg incomingMessage) {
	c.mu.Lock()
	handler := c.requests[msg.Method]
	c.mu.Unlock()

	if handler == nil {
		_ = c.respondError(msg.ID, codeMethodNotFound, fmt.Sprintf("unsupported server request %s", msg.Method))
		return
	}
	result, err := handler(msg.Params)
	if err != nil {
		_ = c.respondError(msg.ID, codeInternalError, err.Error())
		return
	}
	_ = c.respondResult(msg.ID, result)
}

func (c *Client) respondResult(id *json.RawMessage, result any) error {
	if result == nil {
		// A response carries exactly one of result or error, so a null result
		// has to be written rather than omitted.
		result = json.RawMessage("null")
	}
	return c.write(outgoingMessage{JSONRPC: protocolVersion, ID: *id, Result: result})
}

func (c *Client) respondError(id *json.RawMessage, code int, message string) error {
	return c.write(outgoingMessage{JSONRPC: protocolVersion, ID: *id, Error: &rpcError{Code: code, Message: message}})
}

func (c *Client) write(msg outgoingMessage) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("lsp: encode message: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClientClosed
	}
	return WriteMessage(c.conn, raw)
}

func (c *Client) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// fail records the terminal transport error and releases every caller waiting
// on an answer that can no longer arrive.
func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.transport == nil {
		c.transport = err
	}
	pending := c.pending
	c.pending = map[int64]chan rpcResult{}
	c.mu.Unlock()

	for _, ch := range pending {
		select {
		case ch <- rpcResult{err: err}:
		default:
		}
	}
}

func errorFrom(err *rpcError) error {
	if err == nil {
		return nil
	}
	return err
}
