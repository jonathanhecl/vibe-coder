package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestPinnedContextsSurviveSaveLoad(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:         filepath.Join(tmp, "project"),
		SessionsDir: filepath.Join(tmp, "sessions"),
	}
	s := New(cfg)
	s.AddUser("hello")
	s.SetPinnedContexts([]string{"/guides/a.md", "/guides/b.txt"})
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded := New(cfg)
	if err := loaded.Load(s.ID()); err != nil {
		t.Fatalf("load: %v", err)
	}
	pins := loaded.PinnedContexts()
	if len(pins) != 2 || pins[0] != "/guides/a.md" || pins[1] != "/guides/b.txt" {
		t.Fatalf("unexpected pins after load: %v", pins)
	}
}

func TestPinnedContextsSurviveClear(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir()}
	s := New(cfg)
	s.AddUser("hello")
	s.SetPinnedContexts([]string{"/guides/a.md"})
	s.Clear()
	if s.MessageCount() != 0 {
		t.Fatalf("expected empty transcript after clear, got %d", s.MessageCount())
	}
	if pins := s.PinnedContexts(); len(pins) != 1 {
		t.Fatalf("expected pins to survive clear, got %v", pins)
	}
}

func TestDeleteSessionRemovesSidecar(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:         filepath.Join(tmp, "project"),
		SessionsDir: filepath.Join(tmp, "sessions"),
	}
	s := New(cfg)
	s.SetPinnedContexts([]string{"/guides/a.md"})
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	id := s.ID()
	if err := DeleteSession(cfg, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	fresh := New(cfg)
	if err := fresh.Load(id); err == nil {
		t.Fatal("expected load to fail after delete")
	}
}

func TestSaveSkipsRewriteWhenUnchanged(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:         filepath.Join(tmp, "project"),
		SessionsDir: filepath.Join(tmp, "sessions"),
	}
	s := New(cfg)
	s.AddUser("hello")
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	target := filepath.Join(cfg.SessionsDir, s.ID()+".jsonl")
	first, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := s.Save(); err != nil {
		t.Fatalf("second save: %v", err)
	}
	second, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !second.ModTime().Equal(first.ModTime()) {
		t.Fatal("expected unchanged save to skip the rewrite")
	}

	// New content must persist.
	s.AddUser("world")
	if err := s.Save(); err != nil {
		t.Fatalf("third save: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if lines := countLines(string(data)); lines != 2 {
		t.Fatalf("expected 2 persisted messages, got %d", lines)
	}
}

func TestSaveAfterLoadSkipsUnchanged(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:         filepath.Join(tmp, "project"),
		SessionsDir: filepath.Join(tmp, "sessions"),
	}
	s := New(cfg)
	s.AddUser("hello")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	id := s.ID()

	loaded := New(cfg)
	if err := loaded.Load(id); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cfg.SessionsDir, id+".jsonl")
	before, _ := os.Stat(target)
	time.Sleep(20 * time.Millisecond)
	if err := loaded.Save(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(target)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("expected post-load save without changes to skip the rewrite")
	}
}

func countLines(s string) int {
	n := 0
	for _, r := range s {
		if r == '\n' {
			n++
		}
	}
	return n
}

func TestRenderMessagesForSummaryCapsInput(t *testing.T) {
	t.Parallel()
	msgs := make([]Message, 0, 60)
	for i := 0; i < 60; i++ {
		msgs = append(msgs, Message{Role: "user", Content: fmt.Sprintf("message-%02d-", i) + strings.Repeat("x", 2000)})
	}
	got := renderMessagesForSummary(msgs)
	if len(got) > 48*1024+512 {
		t.Fatalf("expected bounded summary input, got %d chars", len(got))
	}
	if !strings.Contains(got, "[earliest history omitted") {
		t.Fatal("expected omission note for clipped input")
	}
	// Tail kept in chronological order: newest message last, oldest dropped.
	if strings.Contains(got, "message-00-") {
		t.Fatal("expected oldest message to be dropped")
	}
	if !strings.Contains(got, "message-59-") {
		t.Fatal("expected newest message to be kept")
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multiple lines, got %d", len(lines))
	}
	if !strings.HasPrefix(lines[len(lines)-1], "user: message-59-") {
		t.Fatalf("expected newest message last, got %q", lines[len(lines)-1][:20])
	}
	if !strings.HasPrefix(lines[1], "user: message-") {
		t.Fatalf("expected chronological order after the note, got %q", lines[1][:20])
	}

	small := renderMessagesForSummary([]Message{{Role: "user", Content: "x"}})
	if small != "user: x\n" {
		t.Fatalf("small input must stay verbatim, got %q", small)
	}
}

func TestSetPinnedContextsSkipsUnchangedRevision(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:         filepath.Join(tmp, "project"),
		SessionsDir: filepath.Join(tmp, "sessions"),
	}
	s := New(cfg)
	s.AddUser("hello")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cfg.SessionsDir, s.ID()+".jsonl")
	before, _ := os.Stat(target)
	time.Sleep(20 * time.Millisecond)

	s.SetPinnedContexts(nil)
	s.SetPinnedContexts([]string{})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(target)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("expected identical pins sync to skip the rewrite")
	}

	s.SetPinnedContexts([]string{"/guides/a.md"})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if pins := s.PinnedContexts(); len(pins) != 1 {
		t.Fatalf("expected pins stored, got %v", pins)
	}
}
