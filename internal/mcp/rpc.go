package mcp

import (
	"context"
	"encoding/json"
	"fmt"
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

func (c *Client) call(ctx context.Context, method string, params any) (any, error) {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.mu.Unlock()

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := c.write(req); err != nil {
		return nil, err
	}
	for {
		resp, err := c.read(ctx)
		if err != nil {
			return nil, err
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp %s error: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *Client) notify(method string, params any) error {
	req := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	return c.write(req)
}

func (c *Client) write(req rpcRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
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

func (c *Client) read(ctx context.Context) (rpcResponse, error) {
	type result struct {
		resp rpcResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		c.mu.Lock()
		reader := c.stdout
		c.mu.Unlock()
		if reader == nil {
			ch <- result{err: fmt.Errorf("mcp stdout unavailable")}
			return
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			ch <- result{err: err}
			return
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			ch <- result{err: fmt.Errorf("decode mcp response: %w", err)}
			return
		}
		ch <- result{resp: resp}
	}()

	select {
	case <-ctx.Done():
		return rpcResponse{}, ctx.Err()
	case res := <-ch:
		return res.resp, res.err
	}
}
