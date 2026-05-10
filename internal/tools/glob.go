package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type GlobTool struct{}

func NewGlobTool() *GlobTool { return &GlobTool{} }

func (t *GlobTool) Name() string { return "Glob" }
func (t *GlobTool) Description() string {
	return "Find files by glob pattern."
}
func (t *GlobTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern":    map[string]any{"type": "string"},
					"path":       map[string]any{"type": "string"},
					"head_limit": map[string]any{"type": "integer"},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

func (t *GlobTool) Execute(ctx context.Context, params map[string]any) Result {
	pattern, ok := params["pattern"].(string)
	if !ok || strings.TrimSpace(pattern) == "" {
		return errResult("pattern is required")
	}
	basePath, _ := params["path"].(string)
	if strings.TrimSpace(basePath) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return errResult(fmt.Sprintf("resolve cwd: %v", err))
		}
		basePath = cwd
	}
	if !filepath.IsAbs(basePath) {
		abs, err := filepath.Abs(basePath)
		if err != nil {
			return errResult(fmt.Sprintf("resolve absolute path: %v", err))
		}
		basePath = abs
	}
	limit := asInt(params["head_limit"], 1000)
	if limit <= 0 {
		limit = 1000
	}

	type item struct {
		path  string
		mtime int64
	}
	matches := make([]item, 0, 128)

	if strings.Contains(pattern, "**") {
		err := filepath.WalkDir(basePath, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return nil
			}
			if d.IsDir() && path != basePath && isIgnoredSearchDir(d.Name()) {
				return filepath.SkipDir
			}
			rel, err := filepath.Rel(basePath, path)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			ok, _ := pathMatchDoubleStar(filepath.ToSlash(pattern), rel)
			if ok {
				info, statErr := d.Info()
				if statErr == nil {
					matches = append(matches, item{path: path, mtime: info.ModTime().UnixNano()})
				}
			}
			return nil
		})
		if err != nil {
			if ctx.Err() != nil {
				return errResult(ctx.Err().Error())
			}
			return errResult(fmt.Sprintf("walk path: %v", err))
		}
	} else {
		expanded := filepath.Join(basePath, pattern)
		paths, err := filepath.Glob(expanded)
		if err != nil {
			return errResult(fmt.Sprintf("glob pattern: %v", err))
		}
		for _, path := range paths {
			if ctx.Err() != nil {
				return errResult(ctx.Err().Error())
			}
			if hasIgnoredPathSegment(basePath, path) {
				continue
			}
			info, err := os.Stat(path)
			if err == nil {
				matches = append(matches, item{path: path, mtime: info.ModTime().UnixNano()})
			}
		}
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].mtime > matches[j].mtime })
	if len(matches) > limit {
		matches = matches[:limit]
	}
	lines := make([]string, 0, len(matches))
	for _, m := range matches {
		lines = append(lines, m.path)
	}
	return Result{Output: strings.Join(lines, "\n")}
}

func hasIgnoredPathSegment(basePath, path string) bool {
	rel, err := filepath.Rel(basePath, path)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if isIgnoredSearchDir(part) {
			return true
		}
	}
	return false
}

func pathMatchDoubleStar(pattern, rel string) (bool, error) {
	if rel == "." {
		rel = ""
	}
	pParts := strings.Split(pattern, "/")
	rParts := strings.Split(rel, "/")
	return matchParts(pParts, rParts), nil
}

func matchParts(pattern, path []string) bool {
	if len(pattern) == 0 {
		return len(path) == 0
	}
	if pattern[0] == "**" {
		if matchParts(pattern[1:], path) {
			return true
		}
		if len(path) > 0 {
			return matchParts(pattern, path[1:])
		}
		return false
	}
	if len(path) == 0 {
		return false
	}
	ok, err := filepath.Match(pattern[0], path[0])
	if err != nil || !ok {
		return false
	}
	return matchParts(pattern[1:], path[1:])
}
