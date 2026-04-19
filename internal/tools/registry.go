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

func (r *Registry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Schemas() []Schema {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.schemas != nil {
		return r.schemas
	}
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]Schema, 0, len(names))
	for _, name := range names {
		result = append(result, r.tools[name].Schema())
	}
	r.schemas = result
	return result
}

func (r *Registry) RegisterDefaults() {
	r.Register(NewReadTool())
	r.Register(NewWriteTool())
	r.Register(NewEditTool())
	r.Register(NewGlobTool())
	r.Register(NewBashTool())
	r.Register(NewGrepTool())
	r.Register(NewWebFetchTool())
	r.Register(NewWebSearchTool())
	r.Register(NewNotebookEditTool())
	r.Register(NewTaskCreateTool())
	r.Register(NewTaskListTool())
	r.Register(NewTaskGetTool())
	r.Register(NewTaskUpdateTool())
	r.Register(NewAskUserQuestionTool())
}
