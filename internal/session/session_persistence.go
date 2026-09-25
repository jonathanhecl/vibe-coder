package session

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxSessionFileBytes = 50 * 1024 * 1024

// maxSessionLineBytes caps a single JSONL message line on load. One message
// (e.g. a verbatim tool observation) can be far larger than the 64KB default
// bufio.Scanner token size, so we expand the buffer to keep sessions loadable.
const maxSessionLineBytes = 16 * 1024 * 1024

var invalidSessionIDChars = regexp.MustCompile(`[^A-Za-z0-9_\-]`)

func (s *Session) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id = newSessionID()
	s.messages = s.messages[:0]
	s.tokenEstimate = 0
	s.revision++
	if s.cfg != nil && s.cfg.Cwd != "" {
		s.projectPath = s.cfg.Cwd
	}
}

func (s *Session) Save() error {
	s.mu.RLock()
	cfg := s.cfg
	id := s.id
	messages := cloneMessages(s.messages)
	pinned := append([]string(nil), s.pinnedContexts...)
	workState := append([]byte(nil), s.workState...)
	rev := s.revision
	savedRev := s.lastSavedRevision
	s.mu.RUnlock()
	if cfg == nil {
		return fmt.Errorf("session config is nil")
	}
	// Nothing changed since the last successful save: skip the full
	// rewrite (transcript + index + sidecar). The REPL saves after every
	// turn, so without this long sessions pay a whole-file rewrite each
	// time even for slash-only turns. Revision 0 with no prior save still
	// writes so empty sessions materialize on disk as before.
	if savedRev != 0 && rev == savedRev {
		return nil
	}
	if err := os.MkdirAll(cfg.SessionsDir, 0o755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}

	target, err := s.sessionFilePath(id)
	if err != nil {
		return err
	}

	if err := writeAtomicFile(cfg.SessionsDir, "*.jsonl.tmp", target, 0o600, func(w io.Writer) error {
		writer := bufio.NewWriter(w)
		enc := json.NewEncoder(writer)
		for _, msg := range messages {
			if err := enc.Encode(msg); err != nil {
				return fmt.Errorf("encode message: %w", err)
			}
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("flush session temp file: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("atomic session replace: %w", err)
	}

	if err := s.writeProjectIndexFor(id); err != nil {
		return err
	}
	if err := s.writeSessionProjectFor(id); err != nil {
		return err
	}
	if err := s.writePinnedContextsFor(id, pinned); err != nil {
		return err
	}
	if err := s.writeWorkStateFor(id, workState); err != nil {
		return err
	}
	s.mu.Lock()
	// Record the snapshot revision, not the current one: messages added
	// concurrently still force the next Save to rewrite.
	if s.revision == rev {
		s.lastSavedRevision = rev
	}
	s.mu.Unlock()
	return nil
}

func (s *Session) Load(id string) error {
	sanitized := sanitizeSessionID(id)
	if sanitized == "" {
		return fmt.Errorf("invalid session id: %q", id)
	}

	path, err := s.sessionFilePath(sanitized)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat session file: %w", err)
	}
	if info.Size() > maxSessionFileBytes {
		return fmt.Errorf("session file too large: %d bytes", info.Size())
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open session file: %w", err)
	}
	defer file.Close()

	loaded := make([]Message, 0, 64)
	scanner := bufio.NewScanner(file)
	// A single message (e.g. a verbatim tool observation) is stored as one
	// JSON line and can exceed the 64KB default token size. Match the
	// expanded-buffer pattern used elsewhere so large sessions stay loadable.
	scanner.Buffer(make([]byte, 0, 64*1024), maxSessionLineBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		loaded = append(loaded, msg)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan session file: %w", err)
	}

	s.mu.RLock()
	sessionsDir := ""
	if s.cfg != nil {
		sessionsDir = s.cfg.SessionsDir
	}
	s.mu.RUnlock()
	pinned := loadPinnedContextsFor(sessionsDir, sanitized)
	workState := loadWorkStateFor(sessionsDir, sanitized)
	loadedProject := loadProjectPathFor(sessionsDir, sanitized)

	s.mu.Lock()
	s.id = sanitized
	s.messages = loaded
	s.pinnedContexts = pinned
	s.workState = workState
	if loadedProject != "" {
		s.projectPath = loadedProject
	} else if s.cfg != nil && s.cfg.Cwd != "" {
		s.projectPath = s.cfg.Cwd
	}
	s.recomputeTokenEstimate()
	s.revision++
	// Just loaded from disk: memory matches the files, so a subsequent
	// Save with no changes is a no-op.
	s.lastSavedRevision = s.revision
	s.mu.Unlock()
	return nil
}

func (s *Session) LoadByProject() (bool, error) {
	indexPath := filepath.Join(s.cfg.SessionsDir, "project-index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read project index: %w", err)
	}

	index := map[string]string{}
	if err := json.Unmarshal(data, &index); err != nil {
		return false, fmt.Errorf("decode project index: %w", err)
	}

	hash, err := cwdHash(s.cfg.Cwd)
	if err != nil {
		return false, err
	}
	id := sanitizeSessionID(index[hash])
	if id == "" {
		return false, nil
	}
	if err := s.Load(id); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Session) sessionFilePath(id string) (string, error) {
	sanitized := sanitizeSessionID(id)
	if sanitized == "" {
		return "", fmt.Errorf("invalid session id: %q", id)
	}

	sessionsDirAbs, err := absPath(s.cfg.SessionsDir)
	if err != nil {
		return "", fmt.Errorf("resolve sessions dir: %w", err)
	}
	path := filepath.Join(s.cfg.SessionsDir, sanitized+".jsonl")
	pathAbs, err := absPath(path)
	if err != nil {
		return "", fmt.Errorf("resolve session path: %w", err)
	}
	if !strings.HasPrefix(pathAbs, sessionsDirAbs+string(filepath.Separator)) && pathAbs != sessionsDirAbs {
		return "", fmt.Errorf("invalid session path outside sessions dir")
	}
	return path, nil
}

func (s *Session) writeProjectIndex() error {
	s.mu.RLock()
	id := s.id
	s.mu.RUnlock()
	return s.writeProjectIndexFor(id)
}

func (s *Session) writeProjectIndexFor(id string) error {
	hash, err := cwdHash(s.cfg.Cwd)
	if err != nil {
		return err
	}
	indexPath := filepath.Join(s.cfg.SessionsDir, "project-index.json")

	index := map[string]string{}
	if existing, err := os.ReadFile(indexPath); err == nil {
		_ = json.Unmarshal(existing, &index)
	}
	index[hash] = id

	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project index: %w", err)
	}
	if err := writeAtomicBytes(s.cfg.SessionsDir, "*.index.tmp", indexPath, 0o600, raw); err != nil {
		return fmt.Errorf("replace index file: %w", err)
	}
	return nil
}

func writeAtomicBytes(dir, pattern, target string, mode os.FileMode, data []byte) error {
	return writeAtomicFile(dir, pattern, target, mode, func(w io.Writer) error {
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("write temp file: %w", err)
		}
		return nil
	})
}

// writeAtomicFile keeps session and index writes crash-safe: callers fill a
// temp file, then we chmod and rename it into place as the final step.
func writeAtomicFile(dir, pattern, target string, mode os.FileMode, write func(io.Writer) error) error {
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func sanitizeSessionID(id string) string {
	clean := invalidSessionIDChars.ReplaceAllString(strings.TrimSpace(id), "")
	if len(clean) > 64 {
		clean = clean[:64]
	}
	return clean
}

// pinnedContextsPath returns the sidecar path holding pinned context paths
// for a session id. The lookup is best-effort: callers treat a missing or
// corrupt sidecar as "no pinned contexts".
func pinnedContextsPath(sessionsDir, id string) (string, error) {
	sanitized := sanitizeSessionID(id)
	if sanitized == "" {
		return "", fmt.Errorf("invalid session id: %q", id)
	}
	sessionsDirAbs, err := absPath(sessionsDir)
	if err != nil {
		return "", fmt.Errorf("resolve sessions dir: %w", err)
	}
	path := filepath.Join(sessionsDir, sanitized+".ctx.json")
	pathAbs, err := absPath(path)
	if err != nil {
		return "", fmt.Errorf("resolve context sidecar path: %w", err)
	}
	if !strings.HasPrefix(pathAbs, sessionsDirAbs+string(filepath.Separator)) && pathAbs != sessionsDirAbs {
		return "", fmt.Errorf("invalid context sidecar path outside sessions dir")
	}
	return path, nil
}

type contextSidecar struct {
	ProjectPath    string   `json:"project_path,omitempty"`
	PinnedContexts []string `json:"pinned_contexts"`
}

func (s *Session) writePinnedContextsFor(id string, pinned []string) error {
	target, err := pinnedContextsPath(s.cfg.SessionsDir, id)
	if err != nil {
		return err
	}
	clean := make([]string, 0, len(pinned))
	for _, p := range pinned {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			clean = append(clean, trimmed)
		}
	}
	s.mu.RLock()
	proj := s.projectPath
	s.mu.RUnlock()
	if proj == "" && s.cfg != nil {
		proj = s.cfg.Cwd
	}
	payload := contextSidecar{
		ProjectPath:    proj,
		PinnedContexts: clean,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pinned contexts: %w", err)
	}
	if err := writeAtomicBytes(s.cfg.SessionsDir, "*.ctx.tmp", target, 0o600, raw); err != nil {
		return fmt.Errorf("replace pinned contexts file: %w", err)
	}
	return nil
}

// loadPinnedContextsFor reads the sidecar for a session id. Missing,
// unreadable, or corrupt sidecars yield an empty list so old sessions
// without pinned context keep working.
func loadPinnedContextsFor(sessionsDir, id string) []string {
	target, err := pinnedContextsPath(sessionsDir, id)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil
	}
	var decoded contextSidecar
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil
	}
	out := make([]string, 0, len(decoded.PinnedContexts))
	for _, p := range decoded.PinnedContexts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// loadProjectPathFor reads the sidecar for a session id. Missing,
// unreadable, or corrupt sidecars yield an empty string.
func loadProjectPathFor(sessionsDir, id string) string {
	target, err := pinnedContextsPath(sessionsDir, id)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return ""
	}
	var decoded contextSidecar
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ""
	}
	return strings.TrimSpace(decoded.ProjectPath)
}

func (s *Session) writeSessionProjectFor(id string) error {
	s.mu.RLock()
	proj := s.projectPath
	s.mu.RUnlock()
	if proj == "" && s.cfg != nil {
		proj = s.cfg.Cwd
	}
	if proj == "" {
		return nil
	}
	return writeSessionProjectPath(s.cfg.SessionsDir, id, proj)
}

func writeSessionProjectPath(sessionsDir, id, projectPath string) error {
	indexPath := filepath.Join(sessionsDir, "session-projects.json")
	index := map[string]string{}
	if existing, err := os.ReadFile(indexPath); err == nil {
		_ = json.Unmarshal(existing, &index)
	}
	index[id] = projectPath
	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session projects: %w", err)
	}
	if err := writeAtomicBytes(sessionsDir, "*.projects.tmp", indexPath, 0o600, raw); err != nil {
		return fmt.Errorf("replace session projects file: %w", err)
	}
	return nil
}

func loadSessionProjects(sessionsDir string) map[string]string {
	indexPath := filepath.Join(sessionsDir, "session-projects.json")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		return map[string]string{}
	}
	var index map[string]string
	if err := json.Unmarshal(raw, &index); err != nil {
		return map[string]string{}
	}
	return index
}

// workStatePath returns the sidecar path holding the opaque durable work
// state for a session id. Like the pinned-contexts sidecar, the lookup is
// best-effort: missing or corrupt files mean "no persisted work state".
func workStatePath(sessionsDir, id string) (string, error) {
	sanitized := sanitizeSessionID(id)
	if sanitized == "" {
		return "", fmt.Errorf("invalid session id: %q", id)
	}
	sessionsDirAbs, err := absPath(sessionsDir)
	if err != nil {
		return "", fmt.Errorf("resolve sessions dir: %w", err)
	}
	path := filepath.Join(sessionsDir, sanitized+".work.json")
	pathAbs, err := absPath(path)
	if err != nil {
		return "", fmt.Errorf("resolve work state path: %w", err)
	}
	if !strings.HasPrefix(pathAbs, sessionsDirAbs+string(filepath.Separator)) && pathAbs != sessionsDirAbs {
		return "", fmt.Errorf("invalid work state path outside sessions dir")
	}
	return path, nil
}

// writeWorkStateFor persists the opaque work-state blob. An empty blob
// removes any stale sidecar so a reset checklist does not resurrect on the
// next resume.
func (s *Session) writeWorkStateFor(id string, raw []byte) error {
	target, err := workStatePath(s.cfg.SessionsDir, id)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove work state sidecar: %w", err)
		}
		return nil
	}
	payload, err := json.Marshal(map[string]json.RawMessage{"work_state": json.RawMessage(raw)})
	if err != nil {
		return fmt.Errorf("encode work state: %w", err)
	}
	if err := writeAtomicBytes(s.cfg.SessionsDir, "*.work.tmp", target, 0o600, payload); err != nil {
		return fmt.Errorf("replace work state file: %w", err)
	}
	return nil
}

// loadWorkStateFor reads the work-state sidecar for a session id. Missing,
// unreadable, or corrupt sidecars yield nil so old sessions keep working.
func loadWorkStateFor(sessionsDir, id string) []byte {
	target, err := workStatePath(sessionsDir, id)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil
	}
	var decoded struct {
		WorkState json.RawMessage `json:"work_state"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil
	}
	if len(decoded.WorkState) == 0 {
		return nil
	}
	return append([]byte(nil), decoded.WorkState...)
}

func newSessionID() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

func cwdHash(cwd string) (string, error) {
	abs, err := absPath(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve cwd for index: %w", err)
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])[:16], nil
}

// absPath rejects paths containing NUL bytes before resolving them. On Unix
// filepath.Abs accepts such paths silently, which would let a corrupt cwd or
// sessions dir produce a bogus hash or an unusable session path.
func absPath(p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("path contains NUL byte")
	}
	return filepath.Abs(p)
}
