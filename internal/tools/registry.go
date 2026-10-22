package tools

import (
	"sort"
	"sync"
)

type Registry struct {
	mu      sync.RWMutex
	tools   map[string]Tool
	schemas []Schema
}

func NewRegistry() *Registry {
	return &Registry{
		tools: map[string]Tool{},
	}
}

func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
	r.schemas = nil
}

// SetOutputLimit applies a shared model-facing output cap (bytes) to every
// registered tool that supports one. Tools registered afterwards keep their
// own defaults, so callers should set the limit once the registry is fully
// populated.
func (r *Registry) SetOutputLimit(maxBytes int) {
	if r == nil || maxBytes <= 0 {
		return
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, t := range r.tools {
		if tl, ok := t.(outputLimiter); ok {
			tl.SetMaxOutputBytes(maxBytes)
		}
	}
}

// SetAsker hands every tool that can ask the user an interactive front end.
// Tools without one keep reading stdin themselves; callers should wire an asker
// whenever a UI already owns the terminal so only one reader touches stdin.
func (r *Registry) SetAsker(a Asker) {
	if r == nil || a == nil {
		return
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, t := range r.tools {
		if at, ok := t.(interface{ SetAsker(Asker) }); ok {
			at.SetAsker(a)
		}
	}
}

func (r *Registry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sortedNamesLocked()
}

func (r *Registry) Schemas() []Schema {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.schemas != nil {
		return r.schemas
	}
	names := r.sortedNamesLocked()
	result := make([]Schema, 0, len(names))
	for _, name := range names {
		result = append(result, r.tools[name].Schema())
	}
	r.schemas = result
	return result
}

func (r *Registry) sortedNamesLocked() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) RegisterDefaults() {
	r.Register(NewReadTool())
	r.Register(NewReadManyTool())
	r.Register(NewWriteTool())
	r.Register(NewEditTool())
	r.Register(NewGlobTool())
	r.Register(NewBashTool())
	r.Register(NewGrepTool())
	r.Register(NewWebFetchTool())
	r.Register(NewWebSearchTool())
	r.Register(NewHTTPRequestTool())
	r.Register(NewNotebookEditTool())
	r.Register(NewTaskCreateTool())
	r.Register(NewTaskListTool())
	r.Register(NewTaskGetTool())
	r.Register(NewTaskUpdateTool())
	r.Register(NewAskUserQuestionTool())
	r.Register(NewTodoWriteTool())
	r.Register(NewInteractiveBashTool())
	r.Register(NewSendInputTool())
	r.Register(NewTerminateSessionTool())
	r.Register(NewGitStatusTool())
	r.Register(NewGitDiffTool())
	r.Register(NewGitUndoTool())
	r.Register(NewBackgroundRunTool())
	r.Register(NewBackgroundStatusTool())
	r.Register(NewBackgroundListTool())
	r.Register(NewBackgroundKillTool())
}
