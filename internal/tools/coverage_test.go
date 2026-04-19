package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestToolMetadataAndRegistryCaches(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.RegisterDefaults()

	names := reg.Names()
	if len(names) == 0 {
		t.Fatal("expected default tools")
	}
	schemas := reg.Schemas()
	if len(schemas) != len(names) {
		t.Fatalf("schemas mismatch: got=%d names=%d", len(schemas), len(names))
	}

	// Hit Description/Schema methods for every default tool.
	for _, name := range names {
		tool := reg.Get(name)
		if tool == nil {
			t.Fatalf("missing tool: %s", name)
		}
		if strings.TrimSpace(tool.Description()) == "" {
			t.Fatalf("empty description: %s", name)
		}
		s := tool.Schema()
		if s.Function.Name == "" {
			t.Fatalf("empty schema function name: %s", name)
		}
	}

	// Cached schemas path.
	_ = reg.Schemas()
}

func TestTaskGetAndUpdatePaths(t *testing.T) {
	t.Parallel()
	create := NewTaskCreateTool()
	get := NewTaskGetTool()
	update := NewTaskUpdateTool()

	created := create.Execute(context.Background(), map[string]any{"content": "cover task"})
	if created.IsError {
		t.Fatalf("create failed: %s", created.Output)
	}
	var task Task
	if err := json.Unmarshal([]byte(created.Output), &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}

	got := get.Execute(context.Background(), map[string]any{"id": task.ID})
	if got.IsError {
		t.Fatalf("get failed: %s", got.Output)
	}
	updated := update.Execute(context.Background(), map[string]any{
		"id":      task.ID,
		"content": "updated content",
		"status":  "done",
		"notes":   "covered",
	})
	if updated.IsError {
		t.Fatalf("update failed: %s", updated.Output)
	}
	if !strings.Contains(updated.Output, "updated content") || !strings.Contains(updated.Output, "covered") {
		t.Fatalf("unexpected update output: %s", updated.Output)
	}

	// Error branches.
	if ok := get.Execute(context.Background(), map[string]any{"id": "missing"}); !ok.IsError {
		t.Fatal("expected missing task get to fail")
	}
	if ok := update.Execute(context.Background(), map[string]any{"id": "missing"}); !ok.IsError {
		t.Fatal("expected missing task update to fail")
	}
}

func TestAskUserAndHelpers(t *testing.T) {
	t.Parallel()

	ask := NewAskUserQuestionTool()
	if out := ask.Execute(context.Background(), map[string]any{}); !out.IsError {
		t.Fatal("expected AskUserQuestion validation error")
	}

	if norm := normalizeCellLanguage("typescript"); norm != "code" {
		t.Fatalf("unexpected normalized language: %s", norm)
	}
	if norm := normalizeCellLanguage("no-such-language"); norm != "code" {
		t.Fatalf("unexpected fallback language: %s", norm)
	}

	matched, err := pathMatchDoubleStar("**/*.go", "internal/tools/file.go")
	if err != nil || !matched {
		t.Fatalf("expected ** match, got matched=%t err=%v", matched, err)
	}
	if matchParts([]string{"*.md"}, []string{"file.go"}) {
		t.Fatal("expected mismatched parts")
	}

	html := "<html><style>body{}</style><script>x()</script><body>Hello\tWorld</body></html>"
	text := htmlToText(html)
	if !strings.Contains(text, "Hello World") {
		t.Fatalf("unexpected htmlToText output: %q", text)
	}
	if !isPrivateHost("127.0.0.1") {
		t.Fatal("expected localhost IP to be private")
	}
}

func TestAskUserHappyPathViaStdinPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	_, _ = w.WriteString("1\n")

	out := NewAskUserQuestionTool().Execute(context.Background(), map[string]any{
		"questions": []any{
			map[string]any{
				"id":     "q1",
				"prompt": "Pick one",
				"options": []any{
					map[string]any{"label": "A"},
				},
			},
		},
	})
	if out.IsError || !strings.Contains(out.Output, `"q1":"1"`) {
		t.Fatalf("unexpected ask output: %s", out.Output)
	}
}

func TestSubAgentMetadataAndParallelValidation(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 2048,
		MaxTokens:     128,
		Temperature:   0.2,
	}
	sub := NewSubAgentTool(cfg, fakeChatClient{})
	par := NewParallelAgentsTool(sub)

	if sub.Name() == "" || sub.Description() == "" || sub.Schema().Function.Name == "" {
		t.Fatal("subagent metadata should not be empty")
	}
	if par.Name() == "" || par.Description() == "" || par.Schema().Function.Name == "" {
		t.Fatal("parallel metadata should not be empty")
	}

	out := par.Execute(context.Background(), map[string]any{"tasks": []any{map[string]any{"prompt": "one"}}})
	if !out.IsError {
		t.Fatal("expected parallel validation error for <2 tasks")
	}
}

func TestWebFetchValidationErrors(t *testing.T) {
	t.Parallel()
	fetch := NewWebFetchTool()
	if out := fetch.Execute(context.Background(), map[string]any{}); !out.IsError {
		t.Fatal("expected url required error")
	}
	if out := fetch.Execute(context.Background(), map[string]any{"url": "ftp://example.com"}); !out.IsError {
		t.Fatal("expected invalid scheme error")
	}
}

func TestWriteAndGlobErrorBranches(t *testing.T) {
	t.Parallel()
	if out := NewWriteTool().Execute(context.Background(), map[string]any{"file_path": "", "contents": "x"}); !out.IsError {
		t.Fatal("expected write validation error")
	}
	if out := NewGlobTool().Execute(context.Background(), map[string]any{"pattern": ""}); !out.IsError {
		t.Fatal("expected glob validation error")
	}

	// Exercise double-star glob traversal branch.
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "a", "b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := filepath.Join(dir, "x.go")
	if err := os.WriteFile(file, []byte("package b"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := NewGlobTool().Execute(context.Background(), map[string]any{
		"pattern": "**/*.go",
		"path":    tmp,
	})
	if out.IsError || !strings.Contains(out.Output, "x.go") {
		t.Fatalf("unexpected glob ** output: %s", out.Output)
	}

	// Non-absolute write path branch.
	if out := NewWriteTool().Execute(context.Background(), map[string]any{
		"file_path": "relative.txt",
		"contents":  "x",
	}); !out.IsError {
		t.Fatal("expected write absolute path validation error")
	}
}

func TestWebFetchPublicURLBestEffort(t *testing.T) {
	t.Parallel()
	out := NewWebFetchTool().Execute(context.Background(), map[string]any{"url": "https://example.com"})
	if out.IsError && !strings.Contains(strings.ToLower(out.Output), "fetch url") {
		t.Fatalf("unexpected webfetch error: %s", out.Output)
	}
}

func TestBashAndWebSearchValidationBranches(t *testing.T) {
	t.Parallel()
	bash := NewBashTool()
	if out := bash.Execute(context.Background(), map[string]any{}); !out.IsError {
		t.Fatal("expected missing command validation error")
	}
	if out := bash.Execute(context.Background(), map[string]any{"command": "echo hi &"}); !out.IsError {
		t.Fatal("expected background command rejection")
	}
	if out := bash.Execute(context.Background(), map[string]any{"command": "rm -rf /"}); !out.IsError {
		t.Fatal("expected dangerous command rejection")
	}

	ws := NewWebSearchTool()
	if out := ws.Execute(context.Background(), map[string]any{}); !out.IsError {
		t.Fatal("expected missing query validation error")
	}
	parsed := parseDDGResults(`<a class="result__a" href="https://example.com">Example <b>Title</b></a>`)
	if len(parsed) != 1 || !strings.Contains(parsed[0], "https://example.com") {
		t.Fatalf("unexpected parseDDGResults output: %#v", parsed)
	}
}

