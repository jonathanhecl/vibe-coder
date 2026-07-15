//go:build rag

package rag

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
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
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks"); err != nil {
		_ = tx.Rollback()
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
			if len(scoredChunks) > k {
				sort.Slice(scoredChunks, func(i, j int) bool { return scoredChunks[i].score > scoredChunks[j].score })
				scoredChunks = scoredChunks[:k]
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(scoredChunks, func(i, j int) bool { return scoredChunks[i].score > scoredChunks[j].score })
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
