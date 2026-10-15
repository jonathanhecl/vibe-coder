package lsp

import (
	"encoding/json"
	"fmt"
)

// protocolVersion is the only JSON-RPC version LSP uses.
const protocolVersion = "2.0"

// JSON-RPC error codes the client itself answers with.
const (
	codeMethodNotFound = -32601
	codeInternalError  = -32603
)

// outgoingMessage is any message the client writes, in either direction:
// requests (ID set, Method set), notifications (ID nil, Method set) and
// responses to a server request (ID set, Result or Error set). One type keeps
// the envelope honest in both directions.
type outgoingMessage struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Method  string    `json:"method,omitempty"`
	Params  any       `json:"params,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

// incomingMessage is any message the client reads. The shape, not a declared
// kind, decides how it is routed: a method with an ID is a request from the
// server, a method without one is a notification, and an ID without a method is
// the answer to a request of ours.
type incomingMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("code %d: %s", e.Code, e.Message)
}

// numericID reads a request ID the client could have sent. LSP allows string
// IDs, so an ID this client cannot have produced is not an answer to it.
func numericID(raw *json.RawMessage) (int64, bool) {
	if raw == nil {
		return 0, false
	}
	var id int64
	if err := json.Unmarshal(*raw, &id); err != nil {
		return 0, false
	}
	return id, true
}
