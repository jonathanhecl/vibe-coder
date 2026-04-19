package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

type sidecarClient struct{}

func (sidecarClient) Tags(context.Context) ([]ollama.Model, error) { return nil, nil }
func (sidecarClient) Version(context.Context) (string, error)      { return "0.0.0", nil }
func (sidecarClient) Pull(context.Context, string, func(ollama.PullEvent)) error {
	return nil
}
func (sidecarClient) Chat(context.Context, ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Done: true}
	close(ch)
	return ch, nil
}
func (sidecarClient) ChatSync(context.Context, ollama.ChatRequest) (ollama.ChatResponse, error) {
	return ollama.ChatResponse{Content: "summary from sidecar"}, nil
}

func TestSetClientClearAndHelpers(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{SessionsDir: t.TempDir(), Cwd: t.TempDir()}
	s := New(cfg)
	s.SetClient(sidecarClient{})
	s.AddUser("hola")
	s.AddAssistant("ok")
	if s.MessageCount() != 2 {
		t.Fatalf("unexpected count before clear: %d", s.MessageCount())
	}
	before := s.ID()
	s.Clear()
	if s.MessageCount() != 0 {
		t.Fatalf("expected clear to reset messages, got %d", s.MessageCount())
	}
	if s.ID() == before {
		t.Fatal("clear should generate a new session ID")
	}

	if got := renderMessagesForSummary([]Message{{Role: "user", Content: "x"}}); !strings.Contains(got, "user: x") {
		t.Fatalf("unexpected summary rendering: %q", got)
	}
}

func TestCompactWithSidecarSummary(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
		ContextWindow: 32,
		SidecarModel:  "sidecar",
	}
	s := New(cfg)
	s.SetClient(sidecarClient{})
	for i := 0; i < 80; i++ {
		s.AddUser(strings.Repeat("z", 40))
	}
	if err := s.Compact(context.Background(), false); err != nil {
		t.Fatalf("compact with sidecar failed: %v", err)
	}
	if s.MessageCount() == 0 || !strings.Contains(s.messages[0].Content, "summary from sidecar") {
		t.Fatalf("expected sidecar summary in first message, got %#v", s.messages)
	}
}

func TestLoadByProjectInvalidIndexAndPathSanitization(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{Cwd: tmp, SessionsDir: filepath.Join(tmp, "sessions")}
	if err := os.MkdirAll(cfg.SessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}
	// Invalid index JSON should fail cleanly.
	if err := os.WriteFile(filepath.Join(cfg.SessionsDir, "project-index.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatalf("write bad index: %v", err)
	}
	s := New(cfg)
	ok, err := s.LoadByProject()
	if err == nil || ok {
		t.Fatalf("expected decode error for invalid index, ok=%t err=%v", ok, err)
	}

	// sanitizeSessionID truncation and cleanup.
	id := sanitizeSessionID("  abcd/../!@#$" + strings.Repeat("x", 100))
	if strings.Contains(id, "/") || len(id) > 64 {
		t.Fatalf("unexpected sanitized id: %q", id)
	}
}

func TestLoadSkipsInvalidLines(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{Cwd: tmp, SessionsDir: filepath.Join(tmp, "sessions")}
	if err := os.MkdirAll(cfg.SessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}
	path := filepath.Join(cfg.SessionsDir, "manual.jsonl")
	valid, _ := json.Marshal(Message{Role: "user", Content: "ok"})
	content := string(valid) + "\n" + "{not-json}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	s := New(cfg)
	if err := s.Load("manual"); err != nil {
		t.Fatalf("load should ignore bad line: %v", err)
	}
	if s.MessageCount() != 1 {
		t.Fatalf("expected exactly one valid line loaded, got %d", s.MessageCount())
	}
}

func TestLoadInvalidIDAndLargeSessionFile(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{Cwd: tmp, SessionsDir: filepath.Join(tmp, "sessions")}
	if err := os.MkdirAll(cfg.SessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}

	s := New(cfg)
	if err := s.Load("../bad"); err == nil {
		t.Fatal("expected invalid ID error")
	}

	huge := filepath.Join(cfg.SessionsDir, "huge.jsonl")
	if err := os.WriteFile(huge, make([]byte, maxSessionFileBytes+1), 0o644); err != nil {
		t.Fatalf("write huge file: %v", err)
	}
	if err := s.Load("huge"); err == nil {
		t.Fatal("expected too-large session file error")
	}
}

func TestLoadByProjectNoFile(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: filepath.Join(t.TempDir(), "sessions")}
	s := New(cfg)
	ok, err := s.LoadByProject()
	if err != nil || ok {
		t.Fatalf("expected no project session without index file, ok=%t err=%v", ok, err)
	}
}

func TestSaveErrorWhenSessionsDirIsAFile(t *testing.T) {
	tmp := t.TempDir()
	sessionsPath := filepath.Join(tmp, "sessions-file")
	if err := os.WriteFile(sessionsPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed sessions path: %v", err)
	}
	cfg := &config.Config{Cwd: tmp, SessionsDir: sessionsPath}
	s := New(cfg)
	s.AddUser("x")
	if err := s.Save(); err == nil {
		t.Fatal("expected save to fail when SessionsDir is not a directory")
	}
}

func TestSessionFilePathValidation(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: filepath.Join(t.TempDir(), "sessions")}
	s := New(cfg)
	if _, err := s.sessionFilePath("!!!"); err == nil {
		t.Fatal("expected invalid session id path error")
	}
}

func TestSaveWithInvalidSessionID(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{Cwd: tmp, SessionsDir: filepath.Join(tmp, "sessions")}
	s := New(cfg)
	s.id = "!!!"
	if err := s.Save(); err == nil {
		t.Fatal("expected save to fail for invalid sanitized session id")
	}
}

func TestWriteProjectIndexPaths(t *testing.T) {
	tmp := t.TempDir()
	sessionsDir := filepath.Join(tmp, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}
	cfg := &config.Config{Cwd: tmp, SessionsDir: sessionsDir}
	s := New(cfg)

	// Existing valid index branch.
	indexPath := filepath.Join(sessionsDir, "project-index.json")
	if err := os.WriteFile(indexPath, []byte(`{"abc":"def"}`), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := s.writeProjectIndex(); err != nil {
		t.Fatalf("writeProjectIndex with existing index failed: %v", err)
	}

	// Existing invalid index should be ignored and overwritten.
	if err := os.WriteFile(indexPath, []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("write broken index: %v", err)
	}
	if err := s.writeProjectIndex(); err != nil {
		t.Fatalf("writeProjectIndex with invalid index failed: %v", err)
	}
}

func TestWriteProjectIndexErrorWhenDirMissing(t *testing.T) {
	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: filepath.Join(t.TempDir(), "missing", "sessions")}
	s := New(cfg)
	if err := s.writeProjectIndex(); err == nil {
		t.Fatal("expected writeProjectIndex to fail when sessions directory is missing")
	}
}

func TestEstimateTextTokensCJK(t *testing.T) {
	t.Parallel()
	if got := estimateTextTokens("こんにちは"); got == 0 {
		t.Fatalf("expected CJK token estimate > 0, got %d", got)
	}
}

func TestSaveRenameFailureWhenTargetIsDirectory(t *testing.T) {
	tmp := t.TempDir()
	sessionsDir := filepath.Join(tmp, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}
	cfg := &config.Config{Cwd: tmp, SessionsDir: sessionsDir}
	s := New(cfg)
	s.AddUser("hello")

	targetDir := filepath.Join(sessionsDir, s.ID()+".jsonl")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := s.Save(); err == nil {
		t.Fatal("expected save rename failure when target path is a directory")
	}
}

func TestWriteProjectIndexRenameFailureWhenTargetIsDirectory(t *testing.T) {
	tmp := t.TempDir()
	sessionsDir := filepath.Join(tmp, "sessions")
	if err := os.MkdirAll(filepath.Join(sessionsDir, "project-index.json"), 0o755); err != nil {
		t.Fatalf("mkdir index-as-dir: %v", err)
	}
	cfg := &config.Config{Cwd: tmp, SessionsDir: sessionsDir}
	s := New(cfg)
	if err := s.writeProjectIndex(); err == nil {
		t.Fatal("expected writeProjectIndex rename failure when index path is a directory")
	}
}

func TestWriteProjectIndexFailsOnInvalidCwd(t *testing.T) {
	tmp := t.TempDir()
	sessionsDir := filepath.Join(tmp, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}
	cfg := &config.Config{Cwd: string([]byte{'b', 'a', 'd', 0}), SessionsDir: sessionsDir}
	s := New(cfg)
	if err := s.writeProjectIndex(); err == nil {
		t.Fatal("expected writeProjectIndex to fail with invalid cwd")
	}
}

