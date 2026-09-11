package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/logger"
)

// runLogEntry is one auditable record of an autonomous turn. It holds only
// counts, status and redacted text so a long run can be reviewed after the
// fact without leaking secrets.
type runLogEntry struct {
	Time    time.Time `json:"time"`
	Session string    `json:"session"`
	Turn    int       `json:"turn"`
	Status  string    `json:"mission_status"`
	Goal    string    `json:"goal,omitempty"`
	Pending int       `json:"pending"`
	Done    int       `json:"done"`
	Tools   int       `json:"tools"`
	Error   string    `json:"error,omitempty"`
}

// RunLogPath returns the per-session JSONL run-log path, or "" when the state
// directory is unavailable (e.g. in tests).
func (a *Agent) RunLogPath() string {
	if a == nil || a.cfg == nil {
		return ""
	}
	dir := strings.TrimSpace(a.cfg.StateDir)
	if dir == "" || a.sess == nil {
		return ""
	}
	return filepath.Join(dir, "runs", safeRunLogName(a.sess.ID())+".jsonl")
}

// LogMissionTurn appends one record describing the turn that just finished.
// It is best-effort: logging must never break an autonomous run.
func (a *Agent) LogMissionTurn(turnErr error) {
	path := a.RunLogPath()
	if path == "" {
		return
	}
	m := a.MissionSnapshot()
	done, pending := a.checklistCounts()
	entry := runLogEntry{
		Time:    time.Now().UTC(),
		Session: a.sess.ID(),
		Turn:    m.Turns,
		Status:  m.Status,
		Goal:    logger.RedactText(strings.TrimSpace(m.Goal)),
		Pending: pending,
		Done:    done,
		Tools:   a.ToolCallsThisTurn(),
	}
	if turnErr != nil {
		entry.Error = logger.RedactText(turnErr.Error())
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logger.Errorf("run log: create dir: %v", err)
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		logger.Errorf("run log: open %s: %v", path, err)
		return
	}
	defer file.Close()
	if _, err := file.Write(append(raw, '\n')); err != nil {
		logger.Errorf("run log: write: %v", err)
	}
}

// checklistCounts returns completed and pending meaningful TODO counts.
func (a *Agent) checklistCounts() (done, pending int) {
	tw := a.todoWriteTool()
	if tw == nil {
		return 0, 0
	}
	for _, it := range tw.Store().Snapshot() {
		if !isMeaningfulTodoContent(it.Content) {
			continue
		}
		switch it.Status {
		case "completed":
			done++
		case "pending", "in_progress":
			pending++
		}
	}
	return done, pending
}

// safeRunLogName keeps the session id usable as a file name.
func safeRunLogName(id string) string {
	id = strings.TrimSpace(id)
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "session"
	}
	return b.String()
}
