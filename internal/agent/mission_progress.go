package agent

import (
	"context"
	"crypto/sha256"
	"regexp"
	"strings"
	"sync"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

const missionEvidenceLimit = 1024

type missionProgressKey struct{}

// MissionProgress tracks recent evidence across turns in one autonomous run.
type MissionProgress struct {
	mu        sync.Mutex
	seen      map[[32]byte]struct{}
	history   [][32]byte
	next      int
	completed map[string]struct{}
	advanced  bool
}

// TrackMissionProgress starts with a fresh evidence window on each user invocation.
func (a *Agent) TrackMissionProgress(ctx context.Context) (context.Context, *MissionProgress) {
	p := &MissionProgress{}
	p.Advanced(a) // Completed checklist items from before this run are the baseline.
	return context.WithValue(ctx, missionProgressKey{}, p), p
}

func (p *MissionProgress) observe(evidence string) {
	key := sha256.Sum256([]byte(evidence))
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.seen[key]; exists {
		return
	}
	if p.seen == nil {
		p.seen = make(map[[32]byte]struct{})
	}
	if len(p.history) < missionEvidenceLimit {
		p.history = append(p.history, key)
	} else {
		delete(p.seen, p.history[p.next])
		p.history[p.next] = key
		p.next = (p.next + 1) % missionEvidenceLimit
	}
	p.seen[key] = struct{}{}
	p.advanced = true
}

// Advanced reports and clears the current turn's progress flag, keeping the evidence window.
// Completed checklist items live outside the bounded tool-evidence window, so
// heavy tool churn cannot evict them and make old items look new again.
func (p *MissionProgress) Advanced(a *Agent) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if tw := a.todoWriteTool(); tw != nil {
		for _, item := range tw.Store().Snapshot() {
			if item.Status == tools.TodoStatusCompleted && isMeaningfulTodoContent(item.Content) {
				content := strings.TrimSpace(item.Content)
				if _, seen := p.completed[content]; !seen {
					if p.completed == nil {
						p.completed = make(map[string]struct{})
					}
					p.completed[content] = struct{}{}
					p.advanced = true
				}
			}
		}
	}
	advanced := p.advanced
	p.advanced = false
	return advanced
}

var goTestTiming = regexp.MustCompile(`(?m)^(ok[ \t]+[^ \t\n]+)[ \t]+(?:\(cached\)|[0-9]+(?:\.[0-9]+)?s)[ \t]*$`)

func observeMissionTool(ctx context.Context, toolName string, params map[string]any, result tools.Result, callSig, outputSig string) {
	p, _ := ctx.Value(missionProgressKey{}).(*MissionProgress)
	if p == nil || result.IsError {
		return
	}
	switch toolName {
	case "TodoWrite", "MissionStart", "MissionComplete", "MissionBlocked", "AskUserQuestion", "SessionNotes", "Decide", "BackgroundList":
		return // Bookkeeping, or polling with volatile elapsed-time metadata.
	case "Bash":
		args := strings.Fields(asString(params["command"]))
		if len(args) >= 2 && args[0] == "go" && args[1] == "test" {
			result.Output = goTestTiming.ReplaceAllString(result.Output, "${1}")
			outputSig = toolOutputSignature(result)
		}
	case "BackgroundStatus":
		callSig = "BackgroundStatus\x00" + asString(params["task_id"])
		lines := strings.Split(result.Output, "\n")
		// Only strip timestamp metadata in the header, not lines of actual job output.
		for i, line := range lines {
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "[elapsed: ") || strings.HasPrefix(line, "[total_duration: ") {
				lines[i] = "[duration omitted]"
			}
		}
		result.Output = strings.Join(lines, "\n")
		outputSig = toolOutputSignature(result)
	}
	p.observe("tool\x00" + callSig + "\x00" + outputSig)
}
