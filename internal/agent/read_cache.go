package agent

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

const (
	defaultReadCacheMaxBytes = 512 * 1024
	maxReadCacheHashBytes    = 4 << 20
)

type readCacheKey struct {
	path      string
	startLine int
	endLine   int
	maxBytes  int
}

type readCacheEntry struct {
	msgIndex    int
	contentHash [32]byte
	truncated   bool
}

// readCache remembers unchanged file Reads whose output is still visible in the
// current wire window so repeated reads of the same unchanged file do not burn
// tool turns and context tokens.
type readCache struct {
	mu      sync.Mutex
	gen     uint64
	entries map[readCacheKey]readCacheEntry
	full    map[string]readCacheEntry
}

func normalizeReadCacheKey(cwd string, defaultMaxBytes int, params map[string]any) (readCacheKey, string, bool) {
	if params == nil {
		return readCacheKey{}, "", false
	}
	rawPath, _ := params["file_path"].(string)
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" || vision.IsImagePath(rawPath) {
		return readCacheKey{}, "", false
	}
	absPath := rawPath
	if !filepath.IsAbs(absPath) && strings.TrimSpace(cwd) != "" {
		absPath = filepath.Join(cwd, absPath)
	}
	absPath = filepath.Clean(absPath)

	startLine := readParamInt(params["start_line"], 0)
	if startLine <= 0 {
		offset := readParamInt(params["offset"], 0)
		if offset < 0 {
			offset = 0
		}
		startLine = offset + 1
	}
	endLine := readParamInt(params["end_line"], 0)
	limit := readParamInt(params["limit"], 0)
	if limit > 0 {
		limitEnd := startLine + limit - 1
		if endLine <= 0 || limitEnd < endLine {
			endLine = limitEnd
		}
	}
	maxBytes := readParamInt(params["max_bytes"], 0)
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
		if maxBytes <= 0 {
			maxBytes = defaultReadCacheMaxBytes
		}
	}
	if maxBytes > defaultReadCacheMaxBytes {
		maxBytes = defaultReadCacheMaxBytes
	}
	return readCacheKey{
		path:      absPath,
		startLine: startLine,
		endLine:   endLine,
		maxBytes:  maxBytes,
	}, rawPath, true
}

func readParamInt(v any, def int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	default:
		return def
	}
}

func hashFile(path string) ([32]byte, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > maxReadCacheHashBytes {
		return [32]byte{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxReadCacheHashBytes+1)); err != nil {
		return [32]byte{}, false
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, true
}

func (a *Agent) readCacheCwdAndLimit() (string, int) {
	cwd := ""
	limit := 0
	if a != nil && a.cfg != nil {
		cwd = a.cfg.Cwd
		limit = tools.OutputLimitBytes(a.cfg)
	}
	return cwd, limit
}

func (a *Agent) isMessageVisibleInWire(msgIndex int, gen uint64) bool {
	if msgIndex < 0 || a.sess == nil || msgIndex >= a.sess.MessageCount() {
		return false
	}
	a.wireMu.Lock()
	defer a.wireMu.Unlock()
	if !a.wire.set || a.wire.gen != gen {
		return true
	}
	if msgIndex < a.wire.start {
		return false
	}
	if a.wire.maskBefore > 0 && msgIndex < a.wire.maskBefore {
		return false
	}
	return true
}

func (a *Agent) checkRedundantRead(params map[string]any) (string, bool) {
	if a == nil || a.sess == nil {
		return "", false
	}
	cwd, defaultMax := a.readCacheCwdAndLimit()
	key, displayPath, ok := normalizeReadCacheKey(cwd, defaultMax, params)
	if !ok {
		return "", false
	}
	gen := a.sess.Generation()

	a.reads.mu.Lock()
	if a.reads.gen != gen {
		a.reads.gen = gen
		a.reads.entries = nil
		a.reads.full = nil
		a.reads.mu.Unlock()
		return "", false
	}
	entry, found := a.reads.entries[key]
	if !found {
		if fullEntry, hasFull := a.reads.full[key.path]; hasFull && !fullEntry.truncated {
			entry = fullEntry
			found = true
		}
	}
	a.reads.mu.Unlock()
	if !found {
		return "", false
	}
	if !a.isMessageVisibleInWire(entry.msgIndex, gen) {
		return "", false
	}
	currentHash, ok := hashFile(key.path)
	if !ok || currentHash != entry.contentHash {
		a.invalidateReadCache(key.path)
		return "", false
	}
	return fmt.Sprintf("[already in context] %s is unchanged since your earlier Read in this conversation. Reuse that content directly to Edit/Write or answer; do not re-read unchanged files.", displayPath), true
}

func (a *Agent) rememberReadResult(params map[string]any, output string, msgIndex int) {
	if a == nil || a.sess == nil || msgIndex < 0 {
		return
	}
	cwd, defaultMax := a.readCacheCwdAndLimit()
	key, _, ok := normalizeReadCacheKey(cwd, defaultMax, params)
	if !ok {
		return
	}
	sum, ok := hashFile(key.path)
	if !ok {
		return
	}
	truncated := strings.Contains(output, "\n[read truncated")
	entry := readCacheEntry{
		msgIndex:    msgIndex,
		contentHash: sum,
		truncated:   truncated,
	}
	gen := a.sess.Generation()

	a.reads.mu.Lock()
	defer a.reads.mu.Unlock()
	if a.reads.gen != gen {
		a.reads.gen = gen
		a.reads.entries = nil
		a.reads.full = nil
	}
	if a.reads.entries == nil {
		a.reads.entries = make(map[readCacheKey]readCacheEntry)
	}
	a.reads.entries[key] = entry
	if key.startLine == 1 && key.endLine == 0 && !truncated {
		if a.reads.full == nil {
			a.reads.full = make(map[string]readCacheEntry)
		}
		a.reads.full[key.path] = entry
	}
}

func (a *Agent) invalidateReadCache(filePath string) {
	if a == nil {
		return
	}
	raw := strings.TrimSpace(filePath)
	if raw == "" {
		return
	}
	cwd, _ := a.readCacheCwdAndLimit()
	abs := raw
	if !filepath.IsAbs(abs) && strings.TrimSpace(cwd) != "" {
		abs = filepath.Join(cwd, abs)
	}
	abs = filepath.Clean(abs)

	a.reads.mu.Lock()
	defer a.reads.mu.Unlock()
	delete(a.reads.full, abs)
	for k := range a.reads.entries {
		if k.path == abs {
			delete(a.reads.entries, k)
		}
	}
}
