package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// An isolated run keeps what it records inside the working directory it was
// started in. The session transcript, the pasted media, the RAG index, and the
// temporary files (a folder-local .vibe-tmp used as TMPDIR) already live there;
// prompt recall comes from the transcript itself, so no history file is written.
// The measured metrics and the logs are not recorded at all, and the settings
// keep going to the shared config file because they describe the user's models
// and providers rather than the run.
const (
	// IsolatedSessionFileName is the folder-local session that makes a
	// directory isolated: a run started in a folder holding one continues in
	// isolated mode. The session store uses the same name.
	IsolatedSessionFileName = ".vibe-isolated.jsonl"
	// legacyIsolatedHistoryFileName is the retired prompt-history file.
	// Isolated runs no longer write it — Up/Down recall is seeded from the
	// session transcript — but an existing file is left untouched: it may
	// hold the only surviving prompts of already-compacted sessions and can
	// be removed manually.
	legacyIsolatedHistoryFileName = ".vibe-isolated-history.txt"
	// LocalTempDirName is the folder-local scratch directory an isolated or
	// temporal run uses instead of a global temp root (/tmp, /var/tmp, the OS
	// temp dir). Scratch files belong to the working directory, never to the
	// machine.
	LocalTempDirName = ".vibe-tmp"
	// sandboxTempEnv is read by code that has to choose a scratch location for
	// itself (a screenshot backend writing through a file, for example) before
	// falling back to a global temp root.
	sandboxTempEnv = "VIBE_SANDBOX_TMPDIR"
)

// Sandboxed reports whether this run may not write outside its working
// directory: an isolated run keeps everything in the folder, and a temporal run
// must leave no trace on the machine.
func (c *Config) Sandboxed() bool {
	return c != nil && (c.Isolated || c.Temporal)
}

// Mode names the run for user- and model-facing text ("isolated", "temporal").
func (c *Config) Mode() string {
	switch {
	case c == nil:
		return ""
	case c.Temporal:
		return "temporal"
	case c.Isolated:
		return "isolated"
	default:
		return ""
	}
}

// LocalTempDir reports the folder-local scratch directory a sandboxed run must
// use, or "" when the run may use the OS temp directory.
func (c *Config) LocalTempDir() string {
	if !c.Sandboxed() {
		return ""
	}
	cwd := strings.TrimSpace(c.Cwd)
	if cwd == "" {
		return ""
	}
	return filepath.Join(cwd, LocalTempDirName)
}

// EnterLocalTemp creates the folder-local scratch directory and points TMPDIR,
// TMP, TEMP and VIBE_SANDBOX_TMPDIR at it, so this process and every command it
// spawns write temporary files inside the working directory instead of a global
// temp root. It is a no-op outside isolated and temporal runs.
func (c *Config) EnterLocalTemp() (string, error) {
	dir := c.LocalTempDir()
	if dir == "" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create local temp dir: %w", err)
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP", sandboxTempEnv} {
		if err := os.Setenv(key, dir); err != nil {
			return "", fmt.Errorf("point %s at the local temp dir: %w", key, err)
		}
	}
	return dir, nil
}

// CleanupLocalTemp removes the scratch directory a sandboxed run created. The
// directory only holds scratch files, so a run that ends leaves nothing behind;
// failure is reported for the debug log but never for the user.
func (c *Config) CleanupLocalTemp() error {
	dir := c.LocalTempDir()
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove local temp dir: %w", err)
	}
	return nil
}

// SandboxTempDir reports the folder-local scratch directory in force for this
// process, read from the environment so packages that never see the config (a
// screenshot backend, a child process) honor it. It is "" outside a sandboxed
// run.
func SandboxTempDir() string {
	return strings.TrimSpace(os.Getenv(sandboxTempEnv))
}

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
	// No history file: prompt recall is seeded from the session transcript,
	// so the input history stays in memory for the run. A legacy
	// .vibe-isolated-history.txt from older runs is left in place — it may
	// be the only record of prompts already compacted away — and can be
	// deleted manually.
	c.HistoryFile = ""
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
