package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
)

type Task struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
	Notes   string `json:"notes,omitempty"`
}

var (
	taskStoreMu sync.Mutex
	taskStore   = map[string]Task{}
	taskSeq     uint64
)

type TaskCreateTool struct{}
type TaskListTool struct{}
type TaskGetTool struct{}
type TaskUpdateTool struct{}

func NewTaskCreateTool() *TaskCreateTool { return &TaskCreateTool{} }
func NewTaskListTool() *TaskListTool     { return &TaskListTool{} }
func NewTaskGetTool() *TaskGetTool       { return &TaskGetTool{} }
func NewTaskUpdateTool() *TaskUpdateTool { return &TaskUpdateTool{} }

func (t *TaskCreateTool) Name() string        { return "TaskCreate" }
func (t *TaskCreateTool) Description() string { return "Create a task." }
func (t *TaskCreateTool) Schema() Schema      { return taskSchema(t.Name(), true) }
func (t *TaskCreateTool) Execute(_ context.Context, params map[string]any) Result {
	content, _ := params["content"].(string)
	if content == "" {
		return errResult("content is required")
	}
	status, _ := params["status"].(string)
	if status == "" {
		status = "pending"
	}
	id := "task-" + intToString(int(atomic.AddUint64(&taskSeq, 1)))
	task := Task{ID: id, Content: content, Status: status}
	taskStoreMu.Lock()
	taskStore[id] = task
	taskStoreMu.Unlock()
	return jsonResult(task)
}

func (t *TaskListTool) Name() string        { return "TaskList" }
func (t *TaskListTool) Description() string { return "List tasks." }
func (t *TaskListTool) Schema() Schema      { return taskSchema(t.Name(), false) }
func (t *TaskListTool) Execute(_ context.Context, _ map[string]any) Result {
	taskStoreMu.Lock()
	defer taskStoreMu.Unlock()
	out := make([]Task, 0, len(taskStore))
	for _, task := range taskStore {
		out = append(out, task)
	}
	return jsonResult(out)
}

func (t *TaskGetTool) Name() string        { return "TaskGet" }
func (t *TaskGetTool) Description() string { return "Get task by id." }
func (t *TaskGetTool) Schema() Schema      { return taskSchema(t.Name(), false) }
func (t *TaskGetTool) Execute(_ context.Context, params map[string]any) Result {
	id, _ := params["id"].(string)
	taskStoreMu.Lock()
	defer taskStoreMu.Unlock()
	task, ok := taskStore[id]
	if !ok {
		return errResult("task not found")
	}
	return jsonResult(task)
}

func (t *TaskUpdateTool) Name() string        { return "TaskUpdate" }
func (t *TaskUpdateTool) Description() string { return "Update task by id." }
func (t *TaskUpdateTool) Schema() Schema      { return taskSchema(t.Name(), false) }
func (t *TaskUpdateTool) Execute(_ context.Context, params map[string]any) Result {
	id, _ := params["id"].(string)
	taskStoreMu.Lock()
	defer taskStoreMu.Unlock()
	task, ok := taskStore[id]
	if !ok {
		return errResult("task not found")
	}
	if v, ok := params["content"].(string); ok && v != "" {
		task.Content = v
	}
	if v, ok := params["status"].(string); ok && v != "" {
		task.Status = v
	}
	if v, ok := params["notes"].(string); ok {
		task.Notes = v
	}
	taskStore[id] = task
	return jsonResult(task)
}

func taskSchema(name string, withContent bool) Schema {
	required := []string{}
	if withContent {
		required = append(required, "content")
	}
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        name,
			Description: "Task tool",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":      map[string]any{"type": "string"},
					"content": map[string]any{"type": "string"},
					"status":  map[string]any{"type": "string"},
					"notes":   map[string]any{"type": "string"},
				},
				"required": required,
			},
		},
	}
}

func jsonResult(v any) Result {
	raw, _ := json.Marshal(v)
	return Result{Output: string(raw)}
}

func intToString(v int) string {
	return strconv.Itoa(v)
}
