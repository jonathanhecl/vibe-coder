//go:build rag

package rag

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// fakeEmbedder returns deterministic vectors derived from the input text so the
// tests can control lexical versus vector ranking.
type fakeEmbedder struct {
	calls int
	err   error
}

func (f *fakeEmbedder) Embed(_ context.Context, model string, inputs []string) ([][]float64, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float64, len(inputs))
	for i, in := range inputs {
		out[i] = fakeVector(in)
	}
	return out, nil
}

// fakeVector maps well-known words to orthogonal axes so a chunk with no lexical
// overlap can still be vector-close to a query.
func fakeVector(text string) []float64 {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "alpha"):
		return []float64{1, 0}
	case strings.Contains(lower, "beta"):
		return []float64{0, 1}
	case strings.Contains(lower, "zebra"):
		return []float64{1, 0}
	default:
		return []float64{0.5, 0.5}
	}
}

// fakeClient satisfies ollama.Client through the embedded (nil) interface and
// adds Embed, so NewEngine's type assertion to ollama.Embedder succeeds.
type fakeClient struct {
	ollama.Client
	emb *fakeEmbedder
}

func (f *fakeClient) Embed(ctx context.Context, model string, inputs []string) ([][]float64, error) {
	return f.emb.Embed(ctx, model, inputs)
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	tmp := t.TempDir()
	return &config.Config{
		Cwd:      tmp,
		StateDir: tmp,
		RAGPath:  filepath.Join(tmp, "rag.db"),
		RoleModels: map[config.Role]config.RoleModel{
			config.RoleEmbedding: {Provider: "ollama", Model: "test-embed"},
		},
	}
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestSplitCodeChunksAtDeclarations(t *testing.T) {
	src := "package main\n\nfunc Alpha() {\n\treturn\n}\n\nfunc Beta() {\n\treturn\n}\n"
	chunks := splitCodeChunks(src, "main.go", chunkSize)
	if len(chunks) < 2 {
		t.Fatalf("expected the file to split at function boundaries, got %d chunk(s)", len(chunks))
	}
	for _, chunk := range chunks {
		head := strings.TrimSpace(strings.SplitN(chunk.Text, "\n", 2)[0])
		if head != "" && !strings.HasPrefix(head, "package ") && !strings.HasPrefix(head, "func ") {
			t.Fatalf("chunk did not start at a declaration boundary: %q", head)
		}
	}
	if chunks[1].Text[0] != 'f' {
		t.Fatalf("second chunk should begin with the second declaration, got %q", chunks[1].Text[:min(20, len(chunks[1].Text))])
	}

	// A non-code file falls back to fixed windows.
	long := strings.Repeat("plain prose without declarations. ", 200)
	text := splitCodeChunks(long, "notes.md", chunkSize)
	if len(text) < 2 {
		t.Fatalf("expected a long non-code file to split into fixed windows, got %d chunk(s)", len(text))
	}
}

func TestIndexPathIsIncrementalAndPrunes(t *testing.T) {
	cfg := testConfig(t)
	src := filepath.Join(cfg.Cwd, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, src, "a.go", "package main\n\nfunc Alpha() {\n\treturn\n}\n")
	writeFile(t, src, "b.go", "package main\n\nfunc Beta() {\n\treturn\n}\n")

	emb := &fakeEmbedder{}
	engine, err := NewEngine(cfg, &fakeClient{emb: emb})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer engine.Close()

	ctx := context.Background()
	if err := engine.IndexPath(ctx, src); err != nil {
		t.Fatalf("first index: %v", err)
	}
	first := engine.Stats()
	if first.Files != 2 || first.Chunks == 0 {
		t.Fatalf("after first index expected 2 files and chunks, got %+v", first)
	}
	if emb.calls == 0 {
		t.Fatal("expected the embedder to be called during the first index")
	}

	// Second run with no changes must not re-embed anything.
	before := emb.calls
	if err := engine.IndexPath(ctx, src); err != nil {
		t.Fatalf("second index: %v", err)
	}
	if emb.calls != before {
		t.Fatalf("unchanged files should not be re-embedded: calls %d -> %d", before, emb.calls)
	}
	if got := engine.Stats(); got.Files != first.Files || got.Chunks != first.Chunks {
		t.Fatalf("unchanged index should be a no-op, stats %+v -> %+v", first, got)
	}

	// Changing one file re-embeds only the changed content.
	writeFile(t, src, "a.go", "package main\n\nfunc Alpha() {\n\tprintln(\"changed\")\n}\n")
	before = emb.calls
	if err := engine.IndexPath(ctx, src); err != nil {
		t.Fatalf("third index: %v", err)
	}
	if emb.calls <= before {
		t.Fatalf("a changed file must be re-embedded: calls %d -> %d", before, emb.calls)
	}

	// Deleting a file prunes its chunks and file row.
	if err := os.Remove(filepath.Join(src, "b.go")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := engine.IndexPath(ctx, src); err != nil {
		t.Fatalf("fourth index: %v", err)
	}
	if got := engine.Stats(); got.Files != 1 {
		t.Fatalf("expected the deleted file to be pruned, stats %+v", got)
	}
	chunks, err := engine.Query(ctx, "beta", 3)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for _, chunk := range chunks {
		if strings.Contains(chunk.File, "b.go") {
			t.Fatalf("deleted file still returned by query: %s", chunk.File)
		}
	}
}

func TestEmbedderFailureDegradesToLexical(t *testing.T) {
	cfg := testConfig(t)
	src := filepath.Join(cfg.Cwd, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, src, "a.go", "package main\n\nfunc Alpha() {\n\treturn\n}\n")

	engine, err := NewEngine(cfg, &fakeClient{emb: &fakeEmbedder{err: errEmbedFailed}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer engine.Close()

	ctx := context.Background()
	if err := engine.IndexPath(ctx, src); err != nil {
		t.Fatalf("indexing must not fail when the embedder errors: %v", err)
	}
	chunks, err := engine.Query(ctx, "alpha", 3)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected lexical retrieval to still work without embeddings")
	}
}

func TestHybridRetrievalSurfacesVectorHit(t *testing.T) {
	cfg := testConfig(t)
	src := filepath.Join(cfg.Cwd, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, src, "a.go", "package main\n\nfunc Alpha() {\n\treturn\n}\n")

	engine, err := NewEngine(cfg, &fakeClient{emb: &fakeEmbedder{}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer engine.Close()

	ctx := context.Background()
	if err := engine.IndexPath(ctx, src); err != nil {
		t.Fatalf("index: %v", err)
	}
	// "zebra" shares no token with the chunk, so lexical scoring is empty; only
	// the vector rank can surface it.
	chunks, err := engine.Query(ctx, "zebra", 3)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(chunks) == 0 || !strings.Contains(chunks[0].Text, "Alpha") {
		t.Fatalf("expected the vector-close chunk to surface via RRF, got %+v", chunks)
	}
}

func TestMigratesLegacyChunksTable(t *testing.T) {
	cfg := testConfig(t)
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(cfg.RAGPath))
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE chunks(id INTEGER PRIMARY KEY, file TEXT NOT NULL, start INT NOT NULL, end INT NOT NULL, text TEXT NOT NULL, tokens TEXT NOT NULL)`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO chunks(file,start,end,text,tokens) VALUES('legacy.go',1,1,'legacy body','[]')`); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	engine, err := NewEngine(cfg, nil)
	if err != nil {
		t.Fatalf("new engine must migrate a legacy db: %v", err)
	}
	defer engine.Close()
	if got := engine.Stats().Chunks; got != 1 {
		t.Fatalf("migration must preserve the legacy row, got %d chunks", got)
	}
}

var errEmbedFailed = &embedError{"embedder offline"}

type embedError struct{ msg string }

func (e *embedError) Error() string { return e.msg }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
