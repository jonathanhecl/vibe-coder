package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func newReadCacheAgent(t *testing.T, ctxWindow int) (*Agent, *session.Session, string) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "test",
		ContextWindow: ctxWindow,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
		StateDir:      filepath.Join(tmp, "state"),
	}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	return New(cfg, fakeClient{}, reg, perm, sess, &fakeUI{}), sess, tmp
}

func TestReadCacheDeduplicatesUnchangedReads(t *testing.T) {
	t.Parallel()
	ag, _, tmp := newReadCacheAgent(t, 32768)
	path := filepath.Join(tmp, "sample.go")
	if err := os.WriteFile(path, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readTool := ag.reg.Get("Read")

	first, _, err := ag.executeTool(context.Background(), readTool, "Read", map[string]any{"file_path": path}, toolExecutionMode{})
	if err != nil || first.IsError || !strings.Contains(first.Output, "package main") {
		t.Fatalf("first Read failed: err=%v out=%q", err, first.Output)
	}

	// Exact same Read while unchanged short-circuits.
	second, _, err := ag.executeTool(context.Background(), readTool, "Read", map[string]any{"file_path": path}, toolExecutionMode{})
	if err != nil || !strings.Contains(second.Output, "[already in context]") {
		t.Fatalf("expected redundant Read short-circuit, got err=%v out=%q", err, second.Output)
	}

	// Sub-range of an already-visible untruncated full Read also short-circuits.
	subRange, _, err := ag.executeTool(context.Background(), readTool, "Read", map[string]any{
		"file_path":  path,
		"start_line": 1,
		"end_line":   1,
	}, toolExecutionMode{})
	if err != nil || !strings.Contains(subRange.Output, "[already in context]") {
		t.Fatalf("expected sub-range Read short-circuit after full Read, got err=%v out=%q", err, subRange.Output)
	}

	// External modification invalidates the cache and serves fresh content.
	if err := os.WriteFile(path, []byte("package main\nfunc updated() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	afterDiskChange, _, err := ag.executeTool(context.Background(), readTool, "Read", map[string]any{"file_path": path}, toolExecutionMode{})
	if err != nil || !strings.Contains(afterDiskChange.Output, "func updated") {
		t.Fatalf("expected fresh Read after disk modification, got err=%v out=%q", err, afterDiskChange.Output)
	}

	// Write/Edit invalidation clears cached reads.
	writeTool := ag.reg.Get("Write")
	if _, _, err := ag.executeTool(context.Background(), writeTool, "Write", map[string]any{
		"file_path": path,
		"contents":  "package main\nfunc rewritten() {}\n",
	}, toolExecutionMode{}); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	afterWrite, _, err := ag.executeTool(context.Background(), readTool, "Read", map[string]any{"file_path": path}, toolExecutionMode{})
	if err != nil || !strings.Contains(afterWrite.Output, "func rewritten") {
		t.Fatalf("expected fresh Read after Write, got err=%v out=%q", err, afterWrite.Output)
	}
}

func TestReadCacheAllowsReReadAfterMaskOrCompaction(t *testing.T) {
	t.Parallel()
	ag, sess, tmp := newReadCacheAgent(t, 32768)
	path := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readTool := ag.reg.Get("Read")

	if _, _, err := ag.executeTool(context.Background(), readTool, "Read", map[string]any{"file_path": path}, toolExecutionMode{}); err != nil {
		t.Fatal(err)
	}

	// Simulate masking past message 0: the earlier Read is no longer visible verbatim.
	ag.wireMu.Lock()
	ag.wire = wireState{
		set:        true,
		gen:        sess.Generation(),
		maskBefore: sess.MessageCount(),
	}
	ag.wireMu.Unlock()

	afterMask, _, err := ag.executeTool(context.Background(), readTool, "Read", map[string]any{"file_path": path}, toolExecutionMode{})
	if err != nil || strings.Contains(afterMask.Output, "[already in context]") || !strings.Contains(afterMask.Output, "hello world") {
		t.Fatalf("expected full Read once earlier read is masked, got err=%v out=%q", err, afterMask.Output)
	}

	// Compaction / Clear bumps generation and resets readCache.
	sess.Clear()
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	afterGen, _, err := ag.executeTool(context.Background(), readTool, "Read", map[string]any{"file_path": path}, toolExecutionMode{})
	if err != nil || strings.Contains(afterGen.Output, "[already in context]") || !strings.Contains(afterGen.Output, "hello world") {
		t.Fatalf("expected full Read after session generation bump, got err=%v out=%q", err, afterGen.Output)
	}
}
