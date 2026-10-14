package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// An isolated run keeps every artifact it produces inside the working
// directory it was started in. The session transcript, the mission run log,
// the metrics, the pasted media, and the RAG index already live there; the
// files below follow the same rule so an isolated run leaves nothing in the
// user-level config and state directories.
const (
	// IsolatedSessionFileName is the folder-local session that makes a
	// directory isolated: a run started in a folder holding one continues in
	// isolated mode. The session store uses the same name.
	IsolatedSessionFileName = ".vibe-isolated.jsonl"
	// IsolatedConfigFileName holds the settings saved from an isolated run:
	// /save, /setmodel persistence, and remembered tool permissions. It is
	// read after the shared config file, so the folder only overrides what
	// was configured from inside it.
	IsolatedConfigFileName = ".vibe-isolated.env"
	// IsolatedHistoryFileName stores submitted prompts for Up/Down recall.
	IsolatedHistoryFileName = ".vibe-isolated-history.txt"
	// IsolatedLogFileName is the debug log of an isolated run.
	IsolatedLogFileName = ".vibe-isolated.log"
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

// EnterIsolatedLayout points every writable path at the isolated working
// directory and reads the folder-local settings file as the last file-level
// source. It is idempotent, so a caller that learns about the isolated mode
// after Load (a folder that already holds an isolated session) can apply it
// again. It is a no-op unless the run is isolated.
func (c *Config) EnterIsolatedLayout() error {
	if c == nil || !c.Isolated {
		return nil
	}
	cwd := strings.TrimSpace(c.Cwd)
	if cwd == "" {
		return errors.New("isolated session requires a working directory")
	}

	c.ConfigFile = filepath.Join(cwd, IsolatedConfigFileName)
	c.PermFile = c.ConfigFile
	c.HistoryFile = filepath.Join(cwd, IsolatedHistoryFileName)

	if _, err := os.Stat(c.ConfigFile); err == nil {
		// Settings saved in this folder count as an existing configuration,
		// so a first run does not prompt for setup on every isolated run.
		c.ConfigFileExists = true
	}
	// The folder file may refine the run but cannot turn isolation off: the
	// paths above already moved, and every write must stay in the folder.
	isolated := c.Isolated
	if err := applyConfigFile(c, c.ConfigFile); err != nil {
		return err
	}
	c.Isolated = isolated
	return nil
}

// LogPath returns the file this run writes its debug log to: the isolated log
// inside the working directory, or vibe-coder.log in the config directory.
// An empty result means logging is not available.
func (c *Config) LogPath() string {
	if c == nil {
		return ""
	}
	if c.Isolated {
		if cwd := strings.TrimSpace(c.Cwd); cwd != "" {
			return filepath.Join(cwd, IsolatedLogFileName)
		}
		return ""
	}
	if dir := strings.TrimSpace(c.ConfigDir); dir != "" {
		return filepath.Join(dir, "vibe-coder.log")
	}
	return ""
}
