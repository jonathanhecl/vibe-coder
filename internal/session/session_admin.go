package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// SessionInfo is a lightweight metadata view of a stored session, suitable
// for listing/picking from the REPL without loading the full transcript.
type SessionInfo struct {
	ID               string
	Path             string
	ModTime          time.Time
	Size             int64
	MessageCount     int
	Preview          string
	IsCurrentProject bool
}

// DeleteSession removes a single saved session file by id and prunes any
// project-index entry that points to it. It is safe to call for ids the
// caller never created (returns os.ErrNotExist), and the index update is
// skipped silently when the index file is missing or already clean.
func DeleteSession(cfg *config.Config, id string) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	sanitized := sanitizeSessionID(id)
	if sanitized == "" {
		return fmt.Errorf("invalid session id: %q", id)
	}
	dir := strings.TrimSpace(cfg.SessionsDir)
	if dir == "" {
		return fmt.Errorf("sessions dir not configured")
	}
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve sessions dir: %w", err)
	}
	target := filepath.Join(dir, sanitized+".jsonl")
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve session path: %w", err)
	}
	if !strings.HasPrefix(targetAbs, dirAbs+string(filepath.Separator)) {
		return fmt.Errorf("invalid session path outside sessions dir")
	}
	if err := os.Remove(target); err != nil {
		return fmt.Errorf("remove session file: %w", err)
	}
	if err := pruneProjectIndex(dir, sanitized); err != nil {
		return err
	}
	return nil
}

// DeleteAllSessions removes every *.jsonl file in cfg.SessionsDir along
// with project-index.json. Returns the number of session files removed.
// Non-session files (logs, configs the user dropped here by mistake) are
// left untouched.
func DeleteAllSessions(cfg *config.Config) (int, error) {
	if cfg == nil {
		return 0, fmt.Errorf("nil config")
	}
	dir := strings.TrimSpace(cfg.SessionsDir)
	if dir == "" {
		return 0, fmt.Errorf("sessions dir not configured")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read sessions dir: %w", err)
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		id := sanitizeSessionID(strings.TrimSuffix(name, ".jsonl"))
		if id == "" {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return removed, fmt.Errorf("remove %s: %w", name, err)
		}
		removed++
	}
	indexPath := filepath.Join(dir, "project-index.json")
	if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
		return removed, fmt.Errorf("remove project index: %w", err)
	}
	return removed, nil
}

// pruneProjectIndex removes every entry whose value matches id. We rewrite
// the file atomically to keep the existing crash-safety guarantees from
// writeProjectIndex. When the index becomes empty we delete the file
// entirely so a fresh project starts from a clean state.
func pruneProjectIndex(dir, id string) error {
	indexPath := filepath.Join(dir, "project-index.json")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read project index: %w", err)
	}
	index := map[string]string{}
	if err := json.Unmarshal(raw, &index); err != nil {
		// Corrupt index — nothing useful to prune; leave it for ListSessions
		// to ignore so we don't accidentally erase recoverable state.
		return nil
	}
	changed := false
	for hash, mapped := range index {
		if mapped == id {
			delete(index, hash)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if len(index) == 0 {
		if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove empty project index: %w", err)
		}
		return nil
	}
	out, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pruned project index: %w", err)
	}
	if err := writeAtomicBytes(dir, "*.index.tmp", indexPath, 0o600, out); err != nil {
		return fmt.Errorf("replace index file: %w", err)
	}
	return nil
}

// ListSessions enumerates persisted sessions in cfg.SessionsDir, returning
// metadata sorted by most recent first. The IsCurrentProject flag is true
// for the session id mapped to the current cwd in project-index.json.
func ListSessions(cfg *config.Config) ([]SessionInfo, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	dir := strings.TrimSpace(cfg.SessionsDir)
	if dir == "" {
		return nil, fmt.Errorf("sessions dir not configured")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	currentProjectID := ""
	if hash, err := cwdHash(cfg.Cwd); err == nil {
		indexPath := filepath.Join(dir, "project-index.json")
		if raw, err := os.ReadFile(indexPath); err == nil {
			index := map[string]string{}
			if json.Unmarshal(raw, &index) == nil {
				currentProjectID = sanitizeSessionID(index[hash])
			}
		}
	}

	out := make([]SessionInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		id := sanitizeSessionID(strings.TrimSuffix(name, ".jsonl"))
		if id == "" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		full := filepath.Join(dir, name)
		count, preview := scanSessionMeta(full)
		out = append(out, SessionInfo{
			ID:               id,
			Path:             full,
			ModTime:          info.ModTime(),
			Size:             info.Size(),
			MessageCount:     count,
			Preview:          preview,
			IsCurrentProject: id == currentProjectID,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].ModTime.After(out[j].ModTime)
	})
	return out, nil
}

// scanSessionMeta reads a session JSONL file once and returns a message
// count plus the first user message preview (trimmed to ~80 chars). It is
// best-effort: any decode error short-circuits with what was collected so
// far so the listing never crashes on a partially-written file.
func scanSessionMeta(path string) (int, string) {
	file, err := os.Open(path)
	if err != nil {
		return 0, ""
	}
	defer file.Close()

	count := 0
	preview := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		count++
		if preview == "" && msg.Role == "user" {
			text := strings.TrimSpace(msg.Content)
			if !strings.HasPrefix(text, "[tool_result") &&
				!strings.HasPrefix(text, "[Earlier conversation summary]") &&
				!strings.HasPrefix(text, "[System Note]") {
				preview = collapseWhitespace(text)
				if len(preview) > 80 {
					preview = preview[:80] + "…"
				}
			}
		}
	}
	return count, preview
}

func collapseWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}
