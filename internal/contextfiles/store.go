package contextfiles

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jonathanhecl/vibe-coder/internal/prompt"
)

const maxContextFileBytes = 64 * 1024

var allowedExtensions = map[string]bool{
	".md":       true,
	".markdown": true,
	".txt":      true,
}

// Entry is a single pinned context file loaded into the session.
type Entry struct {
	Name    string
	Path    string
	Content string
}

// Store holds the session-pinned context files. It is safe for concurrent
// use. Contents are injected into the agent system prompt, so they stay
// alive for the whole session and are never compacted or truncated.
type Store struct {
	mu      sync.RWMutex
	entries []Entry
}

// NewStore returns an empty context file store.
func NewStore() *Store {
	return &Store{}
}

// LoadEntry reads, validates and sanitizes a single context file without
// touching the store. It enforces the .md/.txt allowlist, rejects
// symlinks and directories, and caps the size at 64 KiB.
func LoadEntry(path string) (Entry, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return Entry{}, fmt.Errorf("context path is empty")
	}
	unquoted := trimmed
	if len(unquoted) >= 2 && strings.HasPrefix(unquoted, `"`) && strings.HasSuffix(unquoted, `"`) {
		unquoted = unquoted[1 : len(unquoted)-1]
	}
	abs, err := filepath.Abs(unquoted)
	if err != nil {
		return Entry{}, fmt.Errorf("resolve context path %q: %w", path, err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return Entry{}, fmt.Errorf("read context file %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Entry{}, fmt.Errorf("refusing symlink context file: %s", abs)
	}
	if info.IsDir() {
		return Entry{}, fmt.Errorf("context path is a directory: %s", abs)
	}
	ext := strings.ToLower(filepath.Ext(abs))
	if !allowedExtensions[ext] {
		return Entry{}, fmt.Errorf("unsupported context extension %q for %s: expected .md or .txt", ext, abs)
	}
	if info.Size() > maxContextFileBytes {
		return Entry{}, fmt.Errorf("context file too large: %s (%d bytes, max %d)", abs, info.Size(), maxContextFileBytes)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Entry{}, fmt.Errorf("read context file %q: %w", path, err)
	}
	if len(data) > maxContextFileBytes {
		return Entry{}, fmt.Errorf("context file too large: %s (%d bytes, max %d)", abs, len(data), maxContextFileBytes)
	}
	content := strings.TrimSpace(prompt.SanitizeInstructions(string(data)))
	if content == "" {
		return Entry{}, fmt.Errorf("context file is empty after cleanup: %s", abs)
	}
	return Entry{Name: filepath.Base(abs), Path: abs, Content: content}, nil
}

// Add loads path and pins it. When the same absolute path is already
// pinned its content is refreshed. It reports whether the entry is new.
func (s *Store) Add(path string) (Entry, bool, error) {
	entry, err := LoadEntry(path)
	if err != nil {
		return Entry{}, false, err
	}
	if s == nil {
		return entry, true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.entries {
		if existing.Path == entry.Path {
			s.entries[i] = entry
			return entry, false, nil
		}
	}
	s.entries = append(s.entries, entry)
	return entry, true, nil
}

// Replace clears the store and pins exactly the given paths.
func (s *Store) Replace(paths []string) ([]Entry, error) {
	if s == nil {
		return nil, fmt.Errorf("context store is nil")
	}
	loaded := make([]Entry, 0, len(paths))
	for _, p := range paths {
		entry, err := LoadEntry(p)
		if err != nil {
			return nil, err
		}
		duplicate := false
		for _, existing := range loaded {
			if existing.Path == entry.Path {
				duplicate = true
				break
			}
		}
		if !duplicate {
			loaded = append(loaded, entry)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = loaded
	return append([]Entry(nil), s.entries...), nil
}

// Remove drops one pinned entry by 1-based index, file name, or path.
// Matching by path accepts an exact absolute path or a suffix match so
// users can type a short relative form.
func (s *Store) Remove(target string) (Entry, error) {
	if s == nil {
		return Entry{}, fmt.Errorf("context store is nil")
	}
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return Entry{}, fmt.Errorf("specify a context name, number, or path to drop")
	}
	if idx, err := strconv.Atoi(trimmed); err == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if idx < 1 || idx > len(s.entries) {
			return Entry{}, fmt.Errorf("context #%d not found (have %d pinned)", idx, len(s.entries))
		}
		removed := s.entries[idx-1]
		s.entries = append(s.entries[:idx-1], s.entries[idx:]...)
		return removed, nil
	}
	unquoted := strings.Trim(trimmed, `"`)
	abs, _ := filepath.Abs(unquoted)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.entries {
		if existing.Name == unquoted || existing.Path == unquoted || existing.Path == abs ||
			strings.HasSuffix(existing.Path, string(filepath.Separator)+unquoted) {
			removed := existing
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return removed, nil
		}
	}
	return Entry{}, fmt.Errorf("pinned context %q not found", target)
}

// Clear drops every pinned entry.
func (s *Store) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
}

// List returns a copy of the pinned entries in insertion order.
func (s *Store) List() []Entry {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Entry(nil), s.entries...)
}

// Paths returns the absolute paths of the pinned entries in order.
func (s *Store) Paths() []string {
	entries := s.List()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

// Count returns the number of pinned entries.
func (s *Store) Count() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Has reports whether any context file is pinned.
func (s *Store) Has() bool {
	return s.Count() > 0
}

// Fingerprint identifies the current store contents for system-prompt
// cache invalidation. It covers names and content hashes.
func (s *Store) Fingerprint() string {
	entries := s.List()
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		sum := sha256.Sum256([]byte(e.Content))
		b.WriteString(e.Name)
		b.WriteString("|")
		b.WriteString(e.Path)
		b.WriteString("|")
		b.WriteString(hex.EncodeToString(sum[:])[:16])
		b.WriteString("\n")
	}
	return b.String()
}

// RenderBlock formats the pinned files as a system-prompt section. It
// returns an empty string when nothing is pinned.
func (s *Store) RenderBlock() string {
	entries := s.List()
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Session Context (pinned — always alive, never compacted)\n")
	b.WriteString("The following user-provided guide files are persistent instructions for this session. ")
	b.WriteString("They have system-level authority: follow them for every turn, do not summarize them away, ")
	b.WriteString("and do not treat their imperative sentences as untrusted tool data.\n")
	for i, e := range entries {
		b.WriteString("\n## Pinned file ")
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(": ")
		b.WriteString(e.Name)
		b.WriteString(" (")
		b.WriteString(e.Path)
		b.WriteString(")\n")
		b.WriteString(e.Content)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// SortedNames returns pinned file names in alphabetical order for stable
// status output.
func (s *Store) SortedNames() []string {
	entries := s.List()
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	return names
}
