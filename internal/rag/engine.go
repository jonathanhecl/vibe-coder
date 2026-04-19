//go:build rag

package rag

import (
	"context"
	"database/sql"
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

type Engine struct {
	cfg    *config.Config
	client ollama.Client
	db     *sql.DB
	path   string
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

func NewEngine(cfg *config.Config, cli ollama.Client) (*Engine, error) {
	dbPath := cfg.RAGPath
	if strings.TrimSpace(dbPath) == "" {
		dbPath = filepath.Join(cfg.StateDir, "rag.db")
	}
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

	dsn := "file:" + filepath.ToSlash(dbPath) + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	e := &Engine{cfg: cfg, client: cli, db: db, path: dbPath}
	if err := e.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return e, nil
}

func (e *Engine) initSchema() error {
	_, err := e.db.Exec(`
CREATE TABLE IF NOT EXISTS chunks(
    id INTEGER PRIMARY KEY,
    file TEXT NOT NULL,
    start INT NOT NULL,
    end INT NOT NULL,
    text TEXT NOT NULL,
    tokens TEXT NOT NULL
);`)
	return err
}

func (e *Engine) IndexPath(ctx context.Context, root string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	_, err = e.db.ExecContext(ctx, "DELETE FROM chunks")
	if err != nil {
		return err
	}

	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO chunks(file,start,end,text,tokens) VALUES(?,?,?,?,?)")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	count := 0
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
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(data)
		chunks := splitChunks(text, 800, 200)
		for _, c := range chunks {
			toks := tokenize(c.Text)
			raw, _ := json.Marshal(toks)
			if _, err := stmt.ExecContext(ctx, path, c.Start, c.End, c.Text, string(raw)); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_ = count
	return nil
}

func (e *Engine) Query(ctx context.Context, q string, k int) ([]Chunk, error) {
	if k <= 0 {
		k = 3
	}
	rows, err := e.db.QueryContext(ctx, "SELECT file,start,end,text,tokens FROM chunks")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	queryTokens := tokenize(q)
	type scored struct {
		chunk Chunk
		score float64
	}
	scoredChunks := make([]scored, 0, 64)
	for rows.Next() {
		var c Chunk
		var rawTokens string
		if err := rows.Scan(&c.File, &c.Start, &c.End, &c.Text, &rawTokens); err != nil {
			continue
		}
		var toks []string
		if err := json.Unmarshal([]byte(rawTokens), &toks); err != nil {
			continue
		}
		score := cosineOverlap(queryTokens, toks)
		if score > 0 {
			scoredChunks = append(scoredChunks, scored{chunk: c, score: score})
		}
	}
	sort.Slice(scoredChunks, func(i, j int) bool { return scoredChunks[i].score > scoredChunks[j].score })
	if len(scoredChunks) > k {
		scoredChunks = scoredChunks[:k]
	}
	out := make([]Chunk, 0, len(scoredChunks))
	for _, s := range scoredChunks {
		out = append(out, s.chunk)
	}
	return out, nil
}

func (e *Engine) FormatContext(chunks []Chunk) string {
	if len(chunks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[RAG Context]\n")
	for _, c := range chunks {
		b.WriteString("// ")
		b.WriteString(filepath.ToSlash(c.File))
		b.WriteString(fmt.Sprintf(":%d-%d\n", c.Start, c.End))
		b.WriteString(c.Text)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
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

func isIgnoredDir(name string) bool {
	switch name {
	case ".git", "node_modules", "target", "dist", "vendor", "build", ".vibe-coder":
		return true
	default:
		return false
	}
}

func isAllowedTextFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".py", ".js", ".ts", ".md", ".rs", ".java", ".c", ".cpp", ".h", ".rb", ".php", ".sh", ".yaml", ".yml", ".json", ".toml":
		return true
	default:
		return false
	}
}

func splitChunks(text string, size, overlap int) []Chunk {
	if size <= 0 {
		size = 800
	}
	if overlap < 0 || overlap >= size {
		overlap = 200
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}
	out := make([]Chunk, 0, len(runes)/size+1)
	start := 0
	for start < len(runes) {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		part := string(runes[start:end])
		lineStart := 1 + strings.Count(string(runes[:start]), "\n")
		lineEnd := lineStart + strings.Count(part, "\n")
		out = append(out, Chunk{Start: lineStart, End: lineEnd, Text: part})
		if end == len(runes) {
			break
		}
		start = end - overlap
		if start < 0 {
			start = 0
		}
	}
	return out
}

func tokenize(text string) []string {
	low := strings.ToLower(text)
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return ' '
		}
	}, low)
	fields := strings.Fields(clean)
	if len(fields) == 0 {
		return nil
	}
	uniq := map[string]struct{}{}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		if _, ok := uniq[f]; ok {
			continue
		}
		uniq[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

func cosineOverlap(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	setA := map[string]struct{}{}
	for _, x := range a {
		setA[x] = struct{}{}
	}
	setB := map[string]struct{}{}
	for _, x := range b {
		setB[x] = struct{}{}
	}
	inter := 0.0
	for token := range setA {
		if _, ok := setB[token]; ok {
			inter++
		}
	}
	return inter / (math.Sqrt(float64(len(setA))) * math.Sqrt(float64(len(setB))))
}
