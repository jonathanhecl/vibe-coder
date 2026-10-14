package slash

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

// TestSessionsDeleteIsolatedKeepsGlobalSessions asserts an isolated run only
// clears the session stored in its own folder: saved sessions in the
// user-level state directory are out of reach.
func TestSessionsDeleteIsolatedKeepsGlobalSessions(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "project")
	sessionsDir := filepath.Join(tmp, "sessions")
	for _, dir := range []string{projectDir, sessionsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	sharedCfg := &config.Config{
		Cwd:         projectDir,
		SessionsDir: sessionsDir,
		ConfigDir:   tmp,
		ConfigFile:  filepath.Join(tmp, "vibe-coder.env"),
	}
	global := session.New(sharedCfg)
	global.AddUser("keep me")
	if err := global.Save(); err != nil {
		t.Fatalf("save shared session: %v", err)
	}

	cfg := &config.Config{
		Cwd:         projectDir,
		SessionsDir: sessionsDir,
		ConfigDir:   tmp,
		ConfigFile:  filepath.Join(tmp, "vibe-coder.env"),
		Isolated:    true,
	}
	isolated := session.New(cfg)
	isolated.AddUser("isolated prompt")
	isolated.AddAssistant("isolated answer")
	if err := isolated.Save(); err != nil {
		t.Fatalf("save isolated session: %v", err)
	}
	if !session.HasIsolatedSession(projectDir) {
		t.Fatal("expected the isolated session file in the folder")
	}

	var out bytes.Buffer
	ctx := &Ctx{Cfg: cfg, Session: isolated, Agent: &fakePlanAgent{}, Out: &out}

	handled, shouldExit, err := Dispatch(ctx, "/sessions delete --all")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected isolated delete --all result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "the isolated session") {
		t.Fatalf("expected the isolated-session confirmation, got %q", out.String())
	}
	if !session.HasIsolatedSession(projectDir) {
		t.Error("expected the folder to stay isolated after clearing its session")
	}
	reloaded := session.New(cfg)
	if ok, err := reloaded.LoadIsolated(); err != nil {
		t.Fatalf("load isolated after clear: %v", err)
	} else if ok {
		t.Error("expected no messages to load from the cleared session")
	}
	assertSharedSessionsIntact(t, sharedCfg, global.ID())

	// A session stored outside the folder cannot be deleted from an isolated run.
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/sessions delete "+global.ID())
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected isolated delete result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "only clears its own session") {
		t.Fatalf("expected the isolated refusal, got %q", out.String())
	}
	assertSharedSessionsIntact(t, sharedCfg, global.ID())
}

func assertSharedSessionsIntact(t *testing.T, cfg *config.Config, wantID string) {
	t.Helper()
	infos, err := session.ListSessions(cfg)
	if err != nil {
		t.Fatalf("list shared sessions: %v", err)
	}
	if len(infos) != 1 || infos[0].ID != wantID {
		t.Fatalf("expected only %s to survive, got %#v", wantID, infos)
	}
}
