package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var ErrNoCheckpoint = errors.New("no completed checkpoint found")

type Checkpoint struct {
	cwd         string
	pendingPath string
	pending     *fileCheckpoint
}

type fileCheckpoint struct {
	Label  string             `json:"label"`
	Path   string             `json:"path"`
	Before checkpointState    `json:"before"`
	After  checkpointRevision `json:"after"`
}

func NewCheckpoint(cwd string) *Checkpoint {
	return &Checkpoint{cwd: cwd}
}

func (c *Checkpoint) IsRepo() bool {
	out, err := c.run("rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

func (c *Checkpoint) Create(label string, paths ...string) error {
	if c.pending != nil {
		return fmt.Errorf("previous checkpoint is still pending")
	}
	if !c.IsRepo() {
		return nil
	}
	root, dir, err := c.location()
	if err != nil {
		return err
	}
	if len(paths) != 1 || strings.TrimSpace(paths[0]) == "" {
		return fmt.Errorf("checkpoint requires one file inside the repository")
	}
	path := resolveUnder(c.cwd, strings.TrimSpace(paths[0]))
	rel := relPathsInsideRepo(realPath(root), []string{path})
	if len(rel) != 1 {
		return fmt.Errorf("checkpoint requires one file inside the repository")
	}
	// Snapshot only the edited file without modifying its contents or index.
	// Tracked, untracked, ignored, and not-yet-created files use the same path.
	// Unrelated dirty files and the user's stash list remain untouched.
	target, err := checkpointTarget(root, rel[0])
	if err != nil {
		return err
	}
	before, err := readCheckpointState(target)
	if err != nil {
		return err
	}
	if len(before.Data) > maxCheckpointBytes/2 {
		return fmt.Errorf("checkpoint source exceeds %d bytes", maxCheckpointBytes/2)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, fmt.Sprintf("%020d-*.pending", time.Now().UnixNano()))
	if err != nil {
		return err
	}
	entry := &fileCheckpoint{Label: label, Path: rel[0], Before: before}
	err = json.NewEncoder(file).Encode(entry)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(file.Name())
		return err
	}
	// Keep the pre-image pending until the tool succeeds and its post-image
	// is recorded; failed tool calls cannot replace a usable undo checkpoint.
	c.pendingPath, c.pending = file.Name(), entry
	return nil
}

func (c *Checkpoint) Complete() error {
	if c.pending == nil {
		return nil
	}
	root, _, err := c.location()
	if err != nil {
		return err
	}
	target, err := checkpointTarget(root, c.pending.Path)
	if err != nil {
		return err
	}
	after, err := readCheckpointState(target)
	if err != nil {
		return err
	}
	if after.revision() == c.pending.Before.revision() {
		return c.Discard()
	}
	entry := *c.pending
	entry.After = after.revision()
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	completed := strings.TrimSuffix(c.pendingPath, ".pending") + ".json"
	if err := replaceCheckpointFile(completed, raw, 0o600); err != nil {
		return err
	}
	return c.Discard()
}

func (c *Checkpoint) Discard() error {
	if c.pending == nil {
		return nil
	}
	if err := os.Remove(c.pendingPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	c.pending, c.pendingPath = nil, ""
	return nil
}

// realPath resolves symlinks in the deepest existing ancestor of p and
// rejoins the remaining tail, so paths to not-yet-created files still map to
// the physical location Git reports (e.g. macOS /var -> /private/var).
func realPath(p string) string {
	clean := filepath.Clean(p)
	cur := clean
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return clean
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// resolveUnder maps p (absolute, or relative to base) into a physical path by
// resolving symlinks in the shared base prefix. This lets a logical working
// directory such as macOS /var/... match the physical path Git reports
// (/private/var/...), while symlinks inside the repository stay visible to
// checkpointTarget so they are still rejected.
func resolveUnder(base, p string) string {
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(base, abs)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(base, abs)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return abs
	}
	return filepath.Join(realPath(base), rel)
}

// relPathsInsideRepo converts absolute paths to repository-relative entries,
// dropping anything outside the repo tree or pointing at the tree itself.
func relPathsInsideRepo(cwd string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		rel, err := filepath.Rel(cwd, p)
		if err != nil || !filepath.IsLocal(rel) || rel == "." {
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

func (c *Checkpoint) Rollback() error {
	if !c.IsRepo() {
		return ErrNoCheckpoint
	}
	root, dir, err := c.location()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return ErrNoCheckpoint
	}
	if err != nil {
		return err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		state, err := readCheckpointState(path)
		if err != nil {
			return err
		}
		var saved fileCheckpoint
		if err := json.Unmarshal(state.Data, &saved); err != nil {
			return fmt.Errorf("invalid checkpoint: %w", err)
		}
		target, err := checkpointTarget(root, saved.Path)
		if err != nil {
			return err
		}
		current, err := readCheckpointState(target)
		if err != nil {
			return err
		}
		if current.revision() != saved.After {
			return fmt.Errorf("cannot undo %s: file changed after the agent edit; checkpoint preserved", saved.Path)
		}
		if saved.Before.Exists {
			if err := replaceCheckpointFile(target, saved.Before.Data, saved.Before.Mode.Perm()); err != nil {
				return err
			}
		} else if current.Exists {
			if err := os.Remove(target); err != nil {
				return err
			}
		}
		return os.Remove(path)
	}
	return ErrNoCheckpoint
}

func (c *Checkpoint) location() (string, string, error) {
	root, err := c.run("rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", err
	}
	gitDir, err := c.run("rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", "", err
	}
	return filepath.Clean(strings.TrimSpace(root)), filepath.Join(strings.TrimSpace(gitDir), "vibe-coder-checkpoints"), nil
}

func (c *Checkpoint) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = c.cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
