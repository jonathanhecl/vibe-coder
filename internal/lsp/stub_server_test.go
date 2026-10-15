package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// stubHandler answers one message the stub received. The stub is passed
// explicitly so a handler never captures a variable that is still being
// assigned.
type stubHandler func(s *stubServer, method string, params json.RawMessage, id *json.RawMessage)

type stubMessage struct {
	method string
	params json.RawMessage
}

// stubServer is an in-process language server for the tests. It speaks the real
// framing over a net.Pipe, and it writes its own Content-Length headers by hand
// so a bug in WriteMessage cannot hide behind a stub built on top of it.
type stubServer struct {
	t       *testing.T
	conn    net.Conn
	handler stubHandler

	writeMu sync.Mutex
	mu      sync.Mutex
	seen    []stubMessage
	answers map[int64]chan incomingMessage
	nextID  int64
}

// startStub connects a client to a stub server and closes both when the test
// ends.
func startStub(t *testing.T, handler stubHandler) (*stubServer, *Client) {
	t.Helper()
	clientEnd, serverEnd := net.Pipe()
	s := &stubServer{t: t, conn: serverEnd, handler: handler, answers: map[int64]chan incomingMessage{}, nextID: 100}
	go s.loop()
	client := New(clientEnd)
	t.Cleanup(func() {
		_ = client.Close()
		_ = serverEnd.Close()
	})
	return s, client
}

func (s *stubServer) loop() {
	reader := bufio.NewReader(s.conn)
	for {
		payload, err := ReadMessage(reader)
		if err != nil {
			return
		}
		var msg incomingMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			continue
		}
		if msg.Method == "" && msg.ID != nil {
			// The answer to a request the stub itself sent.
			id, ok := numericID(msg.ID)
			if !ok {
				continue
			}
			s.mu.Lock()
			ch := s.answers[id]
			delete(s.answers, id)
			s.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		s.mu.Lock()
		s.seen = append(s.seen, stubMessage{method: msg.Method, params: msg.Params})
		s.mu.Unlock()
		if s.handler != nil {
			s.handler(s, msg.Method, msg.Params, msg.ID)
		}
	}
}

// write frames one payload. The whole frame is written under a lock because
// net.Pipe splits a frame across two writes and two goroutines writing at once
// would interleave them, which no correct client could parse.
func (s *stubServer) write(payload []byte) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := fmt.Fprintf(s.conn, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return
	}
	_, _ = s.conn.Write(payload)
}

func (s *stubServer) notify(method string, params any) {
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		s.t.Errorf("stub: encode %s: %v", method, err)
		return
	}
	s.write(payload)
}

func (s *stubServer) respond(id *json.RawMessage, result any) {
	if id == nil {
		s.t.Errorf("stub: respond without a request id")
		return
	}
	s.respondRaw(id, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(*id), "result": result})
}

func (s *stubServer) respondError(id *json.RawMessage, code int, message string) {
	if id == nil {
		s.t.Errorf("stub: respondError without a request id")
		return
	}
	s.respondRaw(id, map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(*id),
		"error":   map[string]any{"code": code, "message": message},
	})
}

func (s *stubServer) respondRaw(id *json.RawMessage, message any) {
	payload, err := json.Marshal(message)
	if err != nil {
		s.t.Errorf("stub: encode response: %v", err)
		return
	}
	s.write(payload)
}

// request sends a server-to-client request and returns the channel its answer
// will arrive on. It never blocks, so a handler can use it too.
func (s *stubServer) request(method string, params any) (int64, chan incomingMessage) {
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	ch := make(chan incomingMessage, 1)
	s.answers[id] = ch
	s.mu.Unlock()

	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		s.t.Errorf("stub: encode %s: %v", method, err)
		return id, ch
	}
	s.write(payload)
	return id, ch
}

func (s *stubServer) awaitAnswer(t *testing.T, ch chan incomingMessage) incomingMessage {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for the client to answer a server request")
		return incomingMessage{}
	}
}

// await blocks until the client has sent method. Failing after two seconds of
// polling keeps a hung client a test failure instead of a hung suite.
func (s *stubServer) await(t *testing.T, method string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if s.saw(method) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; saw %v", method, s.methods())
		}
		time.Sleep(time.Millisecond)
	}
}

func (s *stubServer) saw(method string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, msg := range s.seen {
		if msg.method == method {
			return true
		}
	}
	return false
}

func (s *stubServer) methods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.seen))
	for _, msg := range s.seen {
		out = append(out, msg.method)
	}
	return out
}

// paramsFor returns the params of the first message sent with method.
func (s *stubServer) paramsFor(method string) json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, msg := range s.seen {
		if msg.method == method {
			return msg.params
		}
	}
	return nil
}

func decodeParams(t *testing.T, raw json.RawMessage, into any) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("no params recorded")
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode params: %v", err)
	}
}
