package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type ChangeKind int

const (
	ChangeCreated ChangeKind = iota
	ChangeModified
	ChangeDeleted
)

type Change struct {
	Path string
	Kind ChangeKind
}

type fileState struct {
	mtime int64
	size  int64
}

type Watcher struct {
	cwd string

	mu       sync.Mutex
	started  bool
	stopCh   chan struct{}
	baseline map[string]fileState
	pending  []Change
}

var ignoredDirs = map[string]struct{}{
	".git": {}, "node_modules": {}, "dist": {}, "target": {}, "vendor": {}, "build": {}, ".vibe-coder": {},
}

func New(cwd string) *Watcher {
	return &Watcher{
		cwd:      cwd,
		stopCh:   make(chan struct{}),
		baseline: map[string]fileState{},
		pending:  make([]Change, 0, 16),
	}
}

func (w *Watcher) Enabled() bool {
	return strings.TrimSpace(w.cwd) != ""
}

func (w *Watcher) RefreshSnapshot() {
	if !w.Enabled() {
		return
	}
	snapshot := w.scan()
	w.mu.Lock()
	w.baseline = snapshot
	w.pending = w.pending[:0]
	w.mu.Unlock()
}

func (w *Watcher) PendingChanges() []Change {
	if !w.Enabled() {
		return nil
	}
	w.ensureStarted()

	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return nil
	}
	changes := append([]Change(nil), w.pending...)
	w.pending = w.pending[:0]
	return changes
}

func (w *Watcher) Format(changes []Change) string {
	if len(changes) == 0 {
		return ""
	}
	return fmt.Sprintf("[System Note] %d file change(s) detected", len(changes))
}

func (w *Watcher) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.stopCh:
		return
	default:
		close(w.stopCh)
	}
}

func (w *Watcher) ensureStarted() {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	w.started = true
	if len(w.baseline) == 0 {
		w.baseline = w.scan()
	}
	w.mu.Unlock()

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				snapshot := w.scan()
				w.mu.Lock()
				changes := diffSnapshots(w.baseline, snapshot)
				if len(changes) > 10 {
					changes = changes[:10]
				}
				if len(changes) > 0 {
					w.pending = append(w.pending, changes...)
				}
				w.baseline = snapshot
				w.mu.Unlock()
			case <-w.stopCh:
				return
			}
		}
	}()
}

func (w *Watcher) scan() map[string]fileState {
	out := map[string]fileState{}
	fileCount := 0
	_ = filepath.WalkDir(w.cwd, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if _, ignored := ignoredDirs[d.Name()]; ignored && path != w.cwd {
				return filepath.SkipDir
			}
			return nil
		}
		fileCount++
		if fileCount > 5000 {
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out[path] = fileState{mtime: info.ModTime().UnixNano(), size: info.Size()}
		return nil
	})
	return out
}

func diffSnapshots(oldSnap, newSnap map[string]fileState) []Change {
	changes := make([]Change, 0, 16)
	for path, old := range oldSnap {
		now, ok := newSnap[path]
		if !ok {
			changes = append(changes, Change{Path: path, Kind: ChangeDeleted})
			continue
		}
		if old.mtime != now.mtime || old.size != now.size {
			changes = append(changes, Change{Path: path, Kind: ChangeModified})
		}
	}
	for path := range newSnap {
		if _, ok := oldSnap[path]; !ok {
			changes = append(changes, Change{Path: path, Kind: ChangeCreated})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}
