//go:build rag

package rag

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// chunkSize is the maximum size, in runes, of a chunk before it is split further.
const chunkSize = 800

// embedBatchSize bounds how many inputs go into a single embeddings request.
const embedBatchSize = 32

// rrfK is the reciprocal rank fusion constant.
const rrfK = 60

type Engine struct {
	cfg        *config.Config
	client     ollama.Client
	embedder   ollama.Embedder
	embedModel string
	db         *sql.DB
	path       string
}

type Chunk struct {
	File  string
	Start int
	End   int
	Text  string
}

type Stats struct {
	Files     int
	Chunks    int
	DBSizeKiB float64
}

// pendingFile is a changed file waiting to be written once the walk is done.
type pendingFile struct {
	path       string
	hash       string
	chunks     []Chunk
	embeddings [][]float64
}

// chunkEntry is a stored chunk with its lexical tokens and vector.
type chunkEntry struct {
	chunk Chunk
	toks  []string
	emb   []float64
}

func NewEngine(cfg *config.Config, cli ollama.Client) (*Engine, error) {
	dbPath := strings.TrimSpace(cfg.RAGPath)
	inMemory := false
	if dbPath == "" {
		switch {
		case cfg.Isolated && strings.TrimSpace(cfg.Cwd) != "":
			dbPath = filepath.Join(cfg.Cwd, ".vibe-isolated-rag.db")
		case cfg.Temporal:
			// A temporal run leaves no trace, so its index lives in memory.
			dbPath = ":memory:"
			inMemory = true
		default:
			dbPath = filepath.Join(cfg.StateDir, "rag.db")
		}
	}
	if !inMemory {
		if !filepath.IsAbs(dbPath) {
			abs, err := filepath.Abs(dbPath)
			if err != nil {
				return nil, err
			}
			dbPath = abs
		}
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return nil, err
		}
	}

	dsn := "file:" + filepath.ToSlash(dbPath) + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	e := &Engine{cfg: cfg, client: cli, db: db, path: dbPath}
	if emb, ok := cli.(ollama.Embedder); ok {
		e.embedder = emb
	}
	e.embedModel = cfg.RoleModelName(config.RoleEmbedding)
	if err := e.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return e, nil
}

func (e *Engine) initSchema() error {
	if _, err := e.db.Exec(`
CREATE TABLE IF NOT EXISTS chunks(
    id INTEGER PRIMARY KEY,
    file TEXT NOT NULL,
    start INT NOT NULL,
    end INT NOT NULL,
    text TEXT NOT NULL,
    tokens TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS files(
    path TEXT PRIMARY KEY,
    hash TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chunks_file ON chunks(file);`); err != nil {
		return err
	}
	return e.ensureEmbeddingColumn()
}

// ensureEmbeddingColumn migrates a database created before embeddings existed.
func (e *Engine) ensureEmbeddingColumn() error {
	rows, err := e.db.Query("PRAGMA table_info(chunks)")
	if err != nil {
		return err
	}
	defer rows.Close()
	hasEmbedding := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == "embedding" {
			hasEmbedding = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasEmbedding {
		return nil
	}
	_, err = e.db.Exec("ALTER TABLE chunks ADD COLUMN embedding TEXT NOT NULL DEFAULT ''")
	return err
}

// IndexPath indexes root incrementally: files whose content hash is unchanged
// are skipped, changed files are re-chunked and re-embedded, and rows for files
// that no longer exist are removed.
func (e *Engine) IndexPath(ctx context.Context, root string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	known, err := e.loadFileHashes(ctx)
	if err != nil {
		return err
	}

	seen := map[string]struct{}{}
	var pending []pendingFile
	err = filepath.WalkDir(rootAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if isIgnoredDir(name) && path != rootAbs {
				return filepath.SkipDir
			}
			return nil
		}
		if !isAllowedTextFile(path) {
			return nil
		}
		seen[path] = struct{}{}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(data)
		hash := hashText(text)
		if known[path] == hash {
			return nil
		}
		chunks := splitCodeChunks(text, path, chunkSize)
		pending = append(pending, pendingFile{
			path:       path,
			hash:       hash,
			chunks:     chunks,
			embeddings: e.embedChunks(ctx, chunks),
		})
		return nil
	})
	if err != nil {
		return err
	}

	var removed []string
	for path := range known {
		if _, ok := seen[path]; !ok {
			removed = append(removed, path)
		}
	}
	if len(pending) == 0 && len(removed) == 0 {
		return nil
	}
	return e.applyIndex(ctx, pending, removed)
}

func (e *Engine) loadFileHashes(ctx context.Context) (map[string]string, error) {
	rows, err := e.db.QueryContext(ctx, "SELECT path, hash FROM files")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, err
		}
		out[path] = hash
	}
	return out, rows.Err()
}

func (e *Engine) applyIndex(ctx context.Context, pending []pendingFile, removed []string) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	insertChunk, err := tx.PrepareContext(ctx, "INSERT INTO chunks(file,start,end,text,tokens,embedding) VALUES(?,?,?,?,?,?)")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer insertChunk.Close()
	deleteChunks, err := tx.PrepareContext(ctx, "DELETE FROM chunks WHERE file = ?")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer deleteChunks.Close()
	upsertFile, err := tx.PrepareContext(ctx, "INSERT INTO files(path,hash) VALUES(?,?) ON CONFLICT(path) DO UPDATE SET hash=excluded.hash")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer upsertFile.Close()
	deleteFile, err := tx.PrepareContext(ctx, "DELETE FROM files WHERE path = ?")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer deleteFile.Close()

	for _, f := range pending {
		if _, err := deleteChunks.ExecContext(ctx, f.path); err != nil {
			_ = tx.Rollback()
			return err
		}
		for i, c := range f.chunks {
			toks := tokenize(c.Text)
			rawToks, _ := json.Marshal(toks)
			embedding := ""
			if i < len(f.embeddings) && len(f.embeddings[i]) > 0 {
				if raw, err := json.Marshal(f.embeddings[i]); err == nil {
					embedding = string(raw)
				}
			}
			if _, err := insertChunk.ExecContext(ctx, f.path, c.Start, c.End, c.Text, string(rawToks), embedding); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		if _, err := upsertFile.ExecContext(ctx, f.path, f.hash); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	for _, path := range removed {
		if _, err := deleteChunks.ExecContext(ctx, path); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := deleteFile.ExecContext(ctx, path); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// embedChunks returns one vector per chunk, or nil when embeddings are not
// available. An embedding failure degrades to lexical-only indexing; it never
// fails the index.
func (e *Engine) embedChunks(ctx context.Context, chunks []Chunk) [][]float64 {
	if e.embedder == nil || strings.TrimSpace(e.embedModel) == "" || len(chunks) == 0 {
		return nil
	}
	out := make([][]float64, 0, len(chunks))
	for start := 0; start < len(chunks); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		inputs := make([]string, 0, end-start)
		for _, c := range chunks[start:end] {
			inputs = append(inputs, c.Text)
		}
		vecs, err := e.embedder.Embed(ctx, e.embedModel, inputs)
		if err != nil || len(vecs) != len(inputs) {
			return nil
		}
		out = append(out, vecs...)
	}
	return out
}

func (e *Engine) embedQuery(ctx context.Context, q string) []float64 {
	if e.embedder == nil || strings.TrimSpace(e.embedModel) == "" || strings.TrimSpace(q) == "" {
		return nil
	}
	vecs, err := e.embedder.Embed(ctx, e.embedModel, []string{q})
	if err != nil || len(vecs) != 1 {
		return nil
	}
	return vecs[0]
}

// Query retrieves the best chunks for q. With an embedding model it fuses a
// lexical ranking and a vector ranking with reciprocal rank fusion; otherwise it
// returns the lexical ranking alone.
func (e *Engine) Query(ctx context.Context, q string, k int) ([]Chunk, error) {
	if k <= 0 {
		k = 3
	}
	rows, err := e.db.QueryContext(ctx, "SELECT file,start,end,text,tokens,embedding FROM chunks")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var all []chunkEntry
	for rows.Next() {
		var c Chunk
		var rawTokens, rawEmbedding string
		if err := rows.Scan(&c.File, &c.Start, &c.End, &c.Text, &rawTokens, &rawEmbedding); err != nil {
			continue
		}
		var toks []string
		if err := json.Unmarshal([]byte(rawTokens), &toks); err != nil {
			continue
		}
		var emb []float64
		if strings.TrimSpace(rawEmbedding) != "" {
			_ = json.Unmarshal([]byte(rawEmbedding), &emb)
		}
		all = append(all, chunkEntry{chunk: c, toks: toks, emb: emb})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	queryTokens := tokenize(q)
	lexical := rankLexical(all, queryTokens)
	var vector []int
	if qe := e.embedQuery(ctx, q); len(qe) > 0 {
		vector = rankVector(all, qe)
	}

	order := fuseRRF([][]int{lexical, vector}, k)
	out := make([]Chunk, 0, len(order))
	for _, idx := range order {
		out = append(out, all[idx].chunk)
	}
	return out, nil
}

// rankLexical orders entries by lexical overlap, best first, keeping only those
// with a positive score.
func rankLexical(all []chunkEntry, queryTokens []string) []int {
	type scored struct {
		idx   int
		score float64
	}
	hits := make([]scored, 0, len(all))
	for i := range all {
		score := cosineOverlap(queryTokens, all[i].toks)
		if score > 0 {
			hits = append(hits, scored{idx: i, score: score})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	out := make([]int, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.idx)
	}
	return out
}

// rankVector orders entries by cosine similarity to the query vector, best
// first, keeping only those with a stored embedding and a positive similarity.
func rankVector(all []chunkEntry, queryVec []float64) []int {
	type scored struct {
		idx   int
		score float64
	}
	hits := make([]scored, 0, len(all))
	for i := range all {
		if len(all[i].emb) == 0 {
			continue
		}
		score := vectorCosine(queryVec, all[i].emb)
		if score > 0 {
			hits = append(hits, scored{idx: i, score: score})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	out := make([]int, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.idx)
	}
	return out
}

// fuseRRF merges ranked lists with reciprocal rank fusion and returns the top k
// entry indices, best first.
func fuseRRF(lists [][]int, k int) []int {
	scores := map[int]float64{}
	for _, list := range lists {
		for rank, idx := range list {
			scores[idx] += 1.0 / float64(rrfK+rank+1)
		}
	}
	idxs := make([]int, 0, len(scores))
	for idx := range scores {
		idxs = append(idxs, idx)
	}
	sort.SliceStable(idxs, func(a, b int) bool { return scores[idxs[a]] > scores[idxs[b]] })
	if len(idxs) > k {
		idxs = idxs[:k]
	}
	return idxs
}

func (e *Engine) FormatContext(chunks []Chunk) string {
	if len(chunks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[RAG Context]\n")
	for _, c := range chunks {
		b.WriteString(formatChunk(c))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// formatChunk renders one chunk as the block the agent injects, so a caller
// that reranks chunks can rebuild the same shape the engine produces.
func formatChunk(c Chunk) string {
	return "// " + filepath.ToSlash(c.File) + fmt.Sprintf(":%d-%d\n", c.Start, c.End) + c.Text
}

// QueryCandidates returns the retrieved chunks as individual blocks instead of
// one joined context block, so the harness can rerank them before injecting.
func (e *Engine) QueryCandidates(ctx context.Context, query string, k int) ([]string, error) {
	chunks, err := e.Query(ctx, query, k)
	if err != nil {
		return nil, err
	}
	items := make([]string, 0, len(chunks))
	for _, c := range chunks {
		items = append(items, formatChunk(c))
	}
	return items, nil
}

func (e *Engine) QueryText(ctx context.Context, query string, k int) (string, error) {
	chunks, err := e.Query(ctx, query, k)
	if err != nil {
		return "", err
	}
	return e.FormatContext(chunks), nil
}

func (e *Engine) Stats() Stats {
	var chunks int
	_ = e.db.QueryRow("SELECT COUNT(1) FROM chunks").Scan(&chunks)
	var files int
	_ = e.db.QueryRow("SELECT COUNT(DISTINCT file) FROM chunks").Scan(&files)
	sizeKiB := 0.0
	if info, err := os.Stat(e.path); err == nil {
		sizeKiB = float64(info.Size()) / 1024.0
	}
	return Stats{Files: files, Chunks: chunks, DBSizeKiB: sizeKiB}
}

func (e *Engine) Close() error {
	if e.db == nil {
		return nil
	}
	return e.db.Close()
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func vectorCosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
