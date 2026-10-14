package tools

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/sync/errgroup"
)

const (
	defaultReadManyMaxFiles   = 64
	defaultReadManyTotalBytes = 1 << 20
)

// ReadManyTool reads several files in one call. Unlike issuing many separate
// Read calls, it validates and reads the requested paths concurrently and
// returns a single, clearly delimited result. This is the primitive that a
// fast-context/retrieval step can build on when it already knows which files
// to inspect.
type ReadManyTool struct {
	// maxOut caps the per-file output used when max_bytes is omitted.
	maxOut int
}

func NewReadManyTool() *ReadManyTool { return &ReadManyTool{} }

// SetMaxOutputBytes sets the default per-file output cap used when max_bytes
// is absent.
func (t *ReadManyTool) SetMaxOutputBytes(n int) { t.maxOut = n }

func (t *ReadManyTool) Name() string { return "ReadMany" }
func (t *ReadManyTool) Description() string {
	return "Read multiple text files concurrently and return each one with line numbers under its own header. Use this when you already know several files to inspect; use Glob/Grep first when you do not."
}
func (t *ReadManyTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"paths": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Absolute paths of the files to read.",
					},
					"file_paths": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Alias for paths.",
					},
					"files": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Alias for paths.",
					},
					"max_bytes": map[string]any{
						"type":        "integer",
						"description": "Per-file byte cap for the rendered output (default: configured output limit or 512KB).",
					},
					"max_bytes_per_file": map[string]any{
						"type":        "integer",
						"description": "Alias for max_bytes.",
					},
					"per_file_max_bytes": map[string]any{
						"type":        "integer",
						"description": "Alias for max_bytes.",
					},
					"total_bytes": map[string]any{
						"type":        "integer",
						"description": "Maximum number of bytes to return across all files combined (default: 1MB).",
					},
					"max_total_bytes": map[string]any{
						"type":        "integer",
						"description": "Alias for total_bytes.",
					},
					"max_bytes_total": map[string]any{
						"type":        "integer",
						"description": "Alias for total_bytes.",
					},
				},
				"required": []string{},
			},
		},
	}
}

func (t *ReadManyTool) Execute(ctx context.Context, params map[string]any) Result {
	paths := readManyPaths(params)
	if len(paths) == 0 {
		return errResult("paths is required: provide an array of absolute file paths")
	}
	if len(paths) > defaultReadManyMaxFiles {
		return errResult(fmt.Sprintf("too many paths: %d (max %d)", len(paths), defaultReadManyMaxFiles))
	}

	perFileMax := firstIntParam(params,
		"max_bytes", "max_bytes_per_file", "per_file_max_bytes")
	if perFileMax <= 0 {
		perFileMax = t.maxOut
	}
	if perFileMax <= 0 {
		perFileMax = defaultReadMaxBytes
	}
	if perFileMax > defaultReadMaxBytes {
		perFileMax = defaultReadMaxBytes
	}

	totalCap := firstIntParam(params,
		"total_bytes", "max_total_bytes", "max_bytes_total", "total_max_bytes")
	if totalCap <= 0 {
		totalCap = defaultReadManyTotalBytes
	}
	if totalCap > 8*defaultReadMaxBytes {
		totalCap = 8 * defaultReadMaxBytes
	}

	results := make([]string, len(paths))
	sem := make(chan struct{}, 8)
	g, gctx := errgroup.WithContext(ctx)
	for i, p := range paths {
		i, p := i, p
		g.Go(func() error {
			if gctx.Err() != nil {
				return gctx.Err()
			}
			sem <- struct{}{}
			defer func() { <-sem }()
			readParams := map[string]any{
				"file_path": p,
				"max_bytes": perFileMax,
			}
			res := NewReadTool().Execute(gctx, readParams)
			results[i] = res.Output
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return errResult(fmt.Sprintf("read many files: %v", err))
	}

	var b strings.Builder
	total := 0
	for i, p := range paths {
		body := strings.TrimRight(results[i], "\n")
		if body == "" {
			body = "(no output)"
		}
		section := fmt.Sprintf("===== %d/%d: %s =====\n%s\n", i+1, len(paths), p, body)
		if total+len(section) > totalCap {
			remaining := totalCap - total
			if remaining > 0 && remaining < len(section) {
				b.WriteString(section[:remaining])
			}
			fmt.Fprintf(&b, "\n... (ReadMany truncated at %d bytes across %d files) ...\n", totalCap, len(paths))
			return Result{Output: strings.TrimSpace(b.String())}
		}
		b.WriteString(section)
		total += len(section)
	}
	fmt.Fprintf(&b, "\n[ReadMany: %d files, %d bytes]\n", len(paths), total)
	return Result{Output: strings.TrimSpace(b.String())}
}

func readManyPaths(params map[string]any) []string {
	for _, key := range []string{"paths", "file_paths", "files", "filepaths"} {
		if v, ok := params[key]; ok {
			if paths := toStringSlice(v); len(paths) > 0 {
				return paths
			}
		}
	}
	if p, ok := params["file_path"].(string); ok && strings.TrimSpace(p) != "" {
		return []string{strings.TrimSpace(p)}
	}
	if p, ok := params["path"].(string); ok && strings.TrimSpace(p) != "" {
		return []string{strings.TrimSpace(p)}
	}
	return nil
}

func toStringSlice(v any) []string {
	switch val := v.(type) {
	case []any:
		out := make([]string, 0, len(val))
		for _, x := range val {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(val))
		for _, s := range val {
			if strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		if strings.TrimSpace(val) != "" {
			return []string{strings.TrimSpace(val)}
		}
	}
	return nil
}

func firstIntParam(params map[string]any, keys ...string) int {
	for _, key := range keys {
		if n := asInt(params[key], 0); n > 0 {
			return n
		}
	}
	return 0
}
