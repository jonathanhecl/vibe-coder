package contextfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeContextFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadEntryAcceptsMarkdownAndText(t *testing.T) {
	tmp := t.TempDir()
	md := writeContextFile(t, tmp, "guide.md", "# Guide\nFollow these steps.")
	txt := writeContextFile(t, tmp, "notes.txt", "Always run tests.")

	for _, path := range []string{md, txt} {
		entry, err := LoadEntry(path)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}
		if !strings.Contains(entry.Content, "Guide") && !strings.Contains(entry.Content, "tests") {
			t.Fatalf("unexpected content for %s: %q", path, entry.Content)
		}
	}
}

func TestLoadEntryRejectsExtensionSymlinkAndSize(t *testing.T) {
	tmp := t.TempDir()

	if _, err := LoadEntry(writeContextFile(t, tmp, "code.go", "package main")); err == nil {
		t.Fatal("expected extension error for .go")
	}

	link := filepath.Join(tmp, "link.md")
	if err := os.Symlink(writeContextFile(t, tmp, "real.md", "hello"), link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if _, err := LoadEntry(link); err == nil {
		t.Fatal("expected symlink refusal")
	}

	big := make([]byte, maxContextFileBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if _, err := LoadEntry(writeContextFile(t, tmp, "big.md", string(big))); err == nil {
		t.Fatal("expected size cap error")
	}

	if _, err := LoadEntry(writeContextFile(t, tmp, "empty.md", "   \n")); err == nil {
		t.Fatal("expected empty content error")
	}
}

func TestStoreAddIsIdempotentAndRenders(t *testing.T) {
	tmp := t.TempDir()
	path := writeContextFile(t, tmp, "guide.md", "Follow the guide.")
	store := NewStore()

	entry, isNew, err := store.Add(path)
	if err != nil || !isNew {
		t.Fatalf("add: entry=%v new=%t err=%v", entry, isNew, err)
	}
	if _, isNew, err := store.Add(path); err != nil || isNew {
		t.Fatalf("re-add should refresh, got new=%t err=%v", isNew, err)
	}
	if got := store.Count(); got != 1 {
		t.Fatalf("expected 1 pinned, got %d", got)
	}

	block := store.RenderBlock()
	if !strings.Contains(block, "Session Context") || !strings.Contains(block, "Follow the guide.") {
		t.Fatalf("render block missing content: %q", block)
	}
	fp := store.Fingerprint()
	if fp == "" {
		t.Fatal("expected non-empty fingerprint")
	}
}

func TestStoreRemoveByIndexAndName(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore()
	a := writeContextFile(t, tmp, "a.md", "alpha")
	b := writeContextFile(t, tmp, "b.txt", "beta")
	if _, _, err := store.Add(a); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Add(b); err != nil {
		t.Fatal(err)
	}

	removed, err := store.Remove("1")
	if err != nil || removed.Name != "a.md" {
		t.Fatalf("remove by index: %v %+v", err, removed)
	}
	removed, err = store.Remove("b.txt")
	if err != nil || removed.Name != "b.txt" {
		t.Fatalf("remove by name: %v %+v", err, removed)
	}
	if store.Has() {
		t.Fatal("expected empty store")
	}
	if _, err := store.Remove("missing"); err == nil {
		t.Fatal("expected error for unknown entry")
	}
}

func TestStoreReplaceResets(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore()
	first := writeContextFile(t, tmp, "first.md", "one")
	second := writeContextFile(t, tmp, "second.md", "two")
	if _, _, err := store.Add(first); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Replace([]string{second})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "second.md" {
		t.Fatalf("unexpected replace result: %+v", entries)
	}
}
