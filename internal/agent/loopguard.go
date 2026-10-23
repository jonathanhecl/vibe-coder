package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// ErrNoProgress marks a run that kept calling the same tool with byte-for-byte
// identical output. While a mission is active the runtime has no turn limit, so
// without this an unproductive loop would spin forever (and burn tokens).
var ErrNoProgress = errors.New("no progress: repeated identical tool call")

// IsNoProgressErr reports whether err is the repeated-tool-call stop signal.
func IsNoProgressErr(err error) bool {
	return err != nil && errors.Is(err, ErrNoProgress)
}

// noProgressLimit is how many consecutive identical (call, output) pairs are
// tolerated before the run stops. Kept comfortably above common legitimate
// repeats such as polling a job whose status output stays "running" for a few
// polls, so real work is not mistaken for a loop.
const noProgressLimit = 8

// maxNoProgressRetries is how many times one turn restates the task after the
// repeated-call stop fires before giving up. The first trip used to end the run
// outright, which dropped the user back to the prompt with nothing to read and
// no way to tell a stuck model from a crash.
const maxNoProgressRetries = 2

type noProgressGuard struct {
	mu     sync.Mutex
	call   string
	output string
	streak int
}

// observe records one tool outcome and reports whether the stream of identical
// outcomes has reached the limit. Any change (different call or output) resets
// the streak, so real progress clears it.
func (g *noProgressGuard) observe(callSig, outputSig string) (int, bool) {
	if callSig == "" {
		return 0, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if callSig == g.call && outputSig == g.output {
		g.streak++
	} else {
		g.call, g.output, g.streak = callSig, outputSig, 1
	}
	return g.streak, g.streak >= noProgressLimit
}

// reset clears the streak for a new turn. The tolerance belongs to the turn that
// spent it: a streak left over from an earlier request aborts the next one on
// its first — identical — call, which is how one stuck turn poisoned every
// prompt after it.
func (g *noProgressGuard) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.call, g.output, g.streak = "", "", 0
}

// recoverNoProgress turns the repeated-call stop into one more attempt inside the
// same turn: it clears the streak, tells the model exactly what it repeated and
// lets the loop ask again, so the next reply is a different request instead of
// the same dead end. A mission is left alone because the mission loop pauses it
// with a printed reason; retrying there would only burn tokens.
func (a *Agent) recoverNoProgress(ctx context.Context, err error, retries *int) bool {
	if !IsNoProgressErr(err) || a.MissionActive() || *retries >= maxNoProgressRetries {
		return false
	}
	*retries = *retries + 1
	detail := strings.TrimSpace(strings.TrimPrefix(err.Error(), ErrNoProgress.Error()+":"))
	a.noProgress.reset()
	a.ui.ShowToolResult("RECOVERY", "repeated tool call detected; asking the model to change its approach", false, nil)
	message := "Stop repeating: "
	if detail != "" {
		message += strings.TrimSuffix(detail, ".") + ". "
	}
	message += "The same call cannot produce a different result. Change your approach now: re-read the file or the command output, fix the concrete cause, or tell the user in plain text what blocks you."
	a.sess.AddRuntimeReminder(message)
	a.compactBestEffort(ctx)
	return true
}

// noAnswerStopErr explains a turn that stopped without an answer. The session
// and its history are intact, so saying so turns a dead end into a resumable
// stop instead of a silence that reads like a crash.
func noAnswerStopErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w (nothing else ran this turn; the session is intact: send \"continue\" to resume, or tell the model what to change)", err)
}

// toolCallSignature canonicalizes a tool call (name + params) so identical
// repeats compare equal regardless of map ordering. Internal UI-only keys
// (prefixed with "_") are ignored.
func toolCallSignature(name string, params map[string]any) string {
	filtered := make(map[string]any, len(params))
	for k, v := range params {
		if strings.HasPrefix(k, "_") {
			continue
		}
		filtered[k] = v
	}
	raw, err := json.Marshal(filtered)
	if err != nil {
		return name
	}
	return name + "\x00" + string(raw)
}

func toolOutputSignature(result tools.Result) string {
	sum := sha256.Sum256([]byte(result.Output))
	return fmt.Sprintf("%t:%x", result.IsError, sum[:8])
}
