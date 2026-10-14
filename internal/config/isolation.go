package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// An isolated run keeps what it records inside the working directory it was
// started in. The session transcript, the pasted media, and the RAG index
// already live there, and the prompt history joins them; the measured metrics
// and the logs are not recorded at all, and the settings keep going to the
// shared config file because they describe the user's models and providers
// rather than the run.
const (
	// IsolatedSessionFileName is the folder-local session that makes a
	// directory isolated: a run started in a folder holding one continues in
	// isolated mode. The session store uses the same name.
	IsolatedSessionFileName = ".vibe-isolated.jsonl"
	// IsolatedHistoryFileName stores submitted prompts for Up/Down recall.
	IsolatedHistoryFileName = ".vibe-isolated-history.txt"
)

// isolatedSessionPresent reports whether cwd holds an isolated session with
// content. An empty file left behind by /new does not make a folder isolated.
func isolatedSessionPresent(cwd string) bool {
	if strings.TrimSpace(cwd) == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(cwd, IsolatedSessionFileName))
	if err != nil || info.IsDir() || info.Size() == 0 {
		return false
	}
	return true
}

// isolatedEnv reports the isolated setting from the environment, shared by
// applyEnv and the isolated-mode decision so both read the same keys.
func isolatedEnv() (value, set bool) {
	raw := strings.TrimSpace(envFirstNonEmpty("VIBE_CODER_ISOLATED", "VIBEGO_ISOLATED", "ISOLATED"))
	if raw == "" {
		return false, false
	}
	return parseBoolish(raw)
}

// isolatedTemporalEnv reports the temporal setting from the environment. A
// temporal run is never isolated, so the decision below has to see it.
func isolatedTemporalEnv() bool {
	raw := strings.TrimSpace(envFirstNonEmpty("VIBE_CODER_TEMPORAL", "VIBEGO_TEMPORAL", "TEMPORAL"))
	if raw == "" {
		return false
	}
	value, ok := parseBoolish(raw)
	return ok && value
}

// isolatedRequested reports whether this run stays inside its working
// directory: asked for by the environment or the CLI, or implied by a folder
// that already holds an isolated session. Explicit alternatives (--new,
// --temporal, an explicit session id) keep their own mode.
func isolatedRequested(cli cliOptions, cwd string) bool {
	if cli.isolated.set {
		return cli.isolated.value
	}
	if value, set := isolatedEnv(); set {
		return value
	}
	if (cli.newSession.set && cli.newSession.value) || (cli.temporal.set && cli.temporal.value) {
		return false
	}
	if isolatedTemporalEnv() {
		return false
	}
	if cli.sessionID.set && strings.TrimSpace(cli.sessionID.value) != "" {
		return false
	}
	return isolatedSessionPresent(cwd)
}

// EnterIsolatedLayout points the paths an isolated run still records at the
// working directory. It is idempotent, so a caller that learns about the
// isolated mode after Load (a folder that already holds an isolated session)
// can apply it again, and it is a no-op unless the run is isolated.
func (c *Config) EnterIsolatedLayout() error {
	if c == nil || !c.Isolated {
		return nil
	}
	cwd := strings.TrimSpace(c.Cwd)
	if cwd == "" {
		return errors.New("isolated session requires a working directory")
	}
	c.HistoryFile = filepath.Join(cwd, IsolatedHistoryFileName)
	return nil
}

// LogPath returns the file this run writes its debug log to, or "" when the
// run records no log. Isolated and temporal runs leave nothing behind, and the
// log carries CLI arguments, so neither writes one.
func (c *Config) LogPath() string {
	if c == nil || c.Isolated || c.Temporal {
		return ""
	}
	if dir := strings.TrimSpace(c.ConfigDir); dir != "" {
		return filepath.Join(dir, "vibe-coder.log")
	}
	return ""
}
