package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestOutputLimitBytes(t *testing.T) {
	t.Parallel()
	// Auto: 15% of context window, 2000-token floor, times four bytes.
	if got := OutputLimitBytes(&config.Config{ContextWindow: 32768}); got != 4*(32768*15/100) {
		t.Fatalf("auto limit=%d", got)
	}
	if got := OutputLimitBytes(&config.Config{ContextWindow: 4096}); got != 4*2000 {
		t.Fatalf("floor limit=%d, want %d", got, 4*2000)
	}
	cfg := &config.Config{ContextWindow: 32768}
	cfg.Harness.ToolOutputMaxTokens = 1000
	if got := OutputLimitBytes(cfg); got != 4000 {
		t.Fatalf("explicit limit=%d, want 4000", got)
	}
	if got := OutputLimitBytes(nil); got != 0 {
		t.Fatalf("nil cfg limit=%d", got)
	}
}

func TestRegistrySetOutputLimit(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	read := NewReadTool()
	bash := NewBashTool()
	reg.Register(read)
	reg.Register(bash)
	reg.Register(NewTodoWriteTool()) // does not implement the limiter interface
	reg.SetOutputLimit(1234)
	if read.maxOut != 1234 || bash.maxOut != 1234 {
		t.Fatalf("limits not applied: read=%d bash=%d", read.maxOut, bash.maxOut)
	}
}

func TestReadDefaultCapFromSetOutputLimit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	// 200 lines of ~100 bytes each (~20KB total).
	var content strings.Builder
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&content, "%s\n", strings.Repeat("x", 99))
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	read := NewReadTool()
	read.SetMaxOutputBytes(4000)
	res := read.Execute(context.Background(), map[string]any{"file_path": path})
	re := regexp.MustCompile(`\[read truncated at line (\d+); call Read again with start_line=(\d+) to continue\]`)
	m := re.FindStringSubmatch(res.Output)
	if m == nil || m[1] != m[2] {
		t.Fatalf("expected line-aware truncation note in output:\n%s", res.Output[len(res.Output)-200:])
	}
	if len(res.Output) > 5000 {
		t.Fatalf("output not capped: %d bytes", len(res.Output))
	}
}

func TestReadExplicitMaxBytesHonored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	var content strings.Builder
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&content, "%s\n", strings.Repeat("x", 99))
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	read := NewReadTool()
	read.SetMaxOutputBytes(1000) // configured cap must not shrink an explicit param
	res := read.Execute(context.Background(), map[string]any{"file_path": path, "max_bytes": 8000})
	if !strings.Contains(res.Output, "read truncated at line") {
		t.Fatalf("expected truncation note, got tail: %s", res.Output[len(res.Output)-160:])
	}
	if len(res.Output) <= 5000 {
		t.Fatalf("explicit max_bytes was not honored over configured cap: %d bytes", len(res.Output))
	}
}

func TestBashHeadTailHalves(t *testing.T) {
	t.Parallel()
	bash := NewBashTool()
	bash.SetMaxOutputBytes(2000)
	res := bash.Execute(context.Background(), map[string]any{
		"command": "head -c 100000 /dev/zero | tr '\\0' 'A'",
	})
	if len(res.Output) > 2500 {
		t.Fatalf("output not capped: %d bytes", len(res.Output))
	}
	if !strings.Contains(res.Output, "... (truncated) ...") {
		t.Fatal("expected truncation marker")
	}
}

func TestBashDefaultCapUnchanged(t *testing.T) {
	t.Parallel()
	bash := NewBashTool()
	res := bash.Execute(context.Background(), map[string]any{
		"command": "head -c 40000 /dev/zero | tr '\\0' 'A'",
	})
	if len(res.Output) > 31*1024 {
		t.Fatalf("default cap changed: %d bytes", len(res.Output))
	}
}
