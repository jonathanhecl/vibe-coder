package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// The MCP client starts a server as a child process, so the stub server used by
// these tests is this same test binary re-executed with MCP_STUB_SERVER=1. The
// test machine therefore needs no real MCP server installed.
const (
	stubServerEnv = "MCP_STUB_SERVER"
	stubHangEnv   = "MCP_STUB_HANG"
)

// TestStubServerProcess is the stub server entry point. It only does work in
// the re-executed child: in the parent the variable is unset and the test skips.
func TestStubServerProcess(t *testing.T) {
	if os.Getenv(stubServerEnv) != "1" {
		t.Skip("stub MCP server; runs only as a re-executed child process")
	}
	runStubServer()
	// Exit before the test framework writes "PASS" onto the JSON-RPC stream.
	os.Exit(0)
}

// runStubServer speaks newline-delimited JSON-RPC 2.0 on stdin/stdout until the
// parent closes the pipe.
func runStubServer() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	out := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		if len(req.ID) == 0 {
			continue // notification: no response is expected
		}
		if os.Getenv(stubHangEnv) == "1" && req.Method == "tools/call" {
			// Deliberately never answer, so the per-call timeout is exercised.
			continue
		}

		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "stub", "version": "0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{
				map[string]any{"name": "echo", "description": "echoes", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "big", "description": "large output", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "weird.tool name", "description": "odd name", "inputSchema": map[string]any{"type": "object"}},
			}}
		case "tools/call":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(req.Params, &p)
			text := "hello"
			if p.Name == "big" {
				text = strings.Repeat("x", 4096)
			}
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
		default:
			writeStubResponse(out, req.ID, nil, map[string]any{"code": -32601, "message": "method not found"})
			continue
		}
		writeStubResponse(out, req.ID, result, nil)
	}
}

func writeStubResponse(out *bufio.Writer, id json.RawMessage, result, rpcErr any) {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
		"error":   rpcErr,
	})
	if err != nil {
		return
	}
	_, _ = out.Write(append(payload, '\n'))
	_ = out.Flush()
}

// startStubClient launches the stub server as a child process and stops it when
// the test ends.
func startStubClient(t *testing.T, extraEnv ...string) *Client {
	t.Helper()
	env := append([]string{stubServerEnv + "=1"}, extraEnv...)
	c := New("stub", os.Args[0], []string{"-test.run=TestStubServerProcess"}, env)
	if err := c.Start(); err != nil {
		t.Fatalf("start stub server: %v", err)
	}
	t.Cleanup(c.Stop)
	return c
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestClientRestartsAfterStop is the regression test for the retired done
// channel: before the fix, every call after a stop/start pair failed with
// "mcp client stopped".
func TestClientRestartsAfterStop(t *testing.T) {
	c := startStubClient(t)
	ctx := testContext(t)

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if _, err := c.ListTools(ctx); err != nil {
		t.Fatalf("list tools: %v", err)
	}

	c.Stop()
	if err := c.Start(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize after restart: %v", err)
	}
	toolsList, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools after restart: %v", err)
	}
	if len(toolsList) == 0 {
		t.Fatal("expected tools after restart")
	}
}

// TestToolWrapNameIsSanitized covers a server tool name the model-facing schema
// cannot carry as-is.
func TestToolWrapNameIsSanitized(t *testing.T) {
	c := startStubClient(t)
	ctx := testContext(t)
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	defs, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var weird ToolDef
	for _, d := range defs {
		if strings.HasPrefix(d.Name, "weird") {
			weird = d
		}
	}
	if weird.Name == "" {
		t.Fatal("stub server did not advertise the odd tool name")
	}
	w := WrapTool(c, "stub", weird)
	if got, want := w.Name(), "mcp_stub_weird_tool_name"; got != want {
		t.Fatalf("wrapped name = %q, want %q", got, want)
	}
}

// TestToolWrapCallTimeout proves a server that accepts a request and never
// answers cannot hang the turn.
func TestToolWrapCallTimeout(t *testing.T) {
	c := startStubClient(t, stubHangEnv+"=1")
	w := WrapTool(c, "stub", ToolDef{Name: "echo", InputSchema: map[string]any{"type": "object"}})
	w.callTimeout = 300 * time.Millisecond

	start := time.Now()
	res := w.Execute(context.Background(), nil)
	elapsed := time.Since(start)

	if !res.IsError {
		t.Fatalf("expected an error result from a hung call, got %q", res.Output)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("call was not bounded by the timeout (took %s)", elapsed)
	}
	if !strings.Contains(res.Output, "mcp stub tool") {
		t.Fatalf("error output should name the server and tool, got %q", res.Output)
	}
}

// TestToolWrapOutputCap proves MCP output respects the shared tool output cap.
func TestToolWrapOutputCap(t *testing.T) {
	c := startStubClient(t)
	ctx := testContext(t)
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	w := WrapTool(c, "stub", ToolDef{Name: "big", InputSchema: map[string]any{"type": "object"}})
	unbounded := w.Execute(ctx, nil)
	if unbounded.IsError {
		t.Fatalf("unstubbed call failed: %s", unbounded.Output)
	}
	if len(unbounded.Output) != 4096 {
		t.Fatalf("expected the full payload before the cap, got %d bytes", len(unbounded.Output))
	}

	w.SetMaxOutputBytes(200)
	capped := w.Execute(ctx, nil)
	if capped.IsError {
		t.Fatalf("capped call failed: %s", capped.Output)
	}
	if len(capped.Output) > 400 {
		t.Fatalf("output was not capped: %d bytes", len(capped.Output))
	}
	if !strings.Contains(capped.Output, "[mcp output truncated to the tool output cap]") {
		t.Fatalf("capped output should state the truncation, got %q", capped.Output)
	}
}
