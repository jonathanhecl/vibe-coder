package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type rpcResult struct {
	resp rpcResponse
	err  error
}

func (c *Client) call(ctx context.Context, method string, params any) (any, error) {
	c.mu.Lock()
	if c.stopped || c.stdin == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp client not started")
	}
	id := c.nextID
	c.nextID++
	responseCh := make(chan rpcResult, 1)
	c.pending[id] = responseCh
	c.mu.Unlock()

	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	if err := c.write(req); err != nil {
		c.removePending(id)
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.removePending(id)
		return nil, ctx.Err()
	case <-c.done:
		c.removePending(id)
		return nil, fmt.Errorf("mcp client stopped")
	case result := <-responseCh:
		if result.err != nil {
			return nil, result.err
		}
		if result.resp.Error != nil {
			return nil, fmt.Errorf("mcp %s error: %s", method, result.resp.Error.Message)
		}
		return result.resp.Result, nil
	}
}

func (c *Client) notify(method string, params any) error {
	return c.write(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *Client) write(req rpcRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil || c.stopped {
		return fmt.Errorf("mcp client not started")
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := c.stdin.Write(append(raw, '\n')); err != nil {
		return err
	}
	return c.stdin.Flush()
}

func (c *Client) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) readLoop() {
	c.mu.Lock()
	reader := c.stdout
	c.mu.Unlock()
	if reader == nil {
		c.failPending(fmt.Errorf("mcp stdout unavailable"))
		return
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				c.failPending(fmt.Errorf("read mcp response: %w", err))
			} else {
				c.failPending(fmt.Errorf("mcp server closed stdout"))
			}
			return
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		if resp.ID == 0 {
			continue
		}
		c.mu.Lock()
		responseCh := c.pending[resp.ID]
		if responseCh != nil {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()
		if responseCh != nil {
			responseCh <- rpcResult{resp: resp}
		}
	}
}

func (c *Client) failPending(err error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[int64]chan rpcResult)
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- rpcResult{err: err}
	}
}
