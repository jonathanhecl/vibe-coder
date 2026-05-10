package tools

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type GrepTool struct{}

func NewGrepTool() *GrepTool { return &GrepTool{} }

func (t *GrepTool) Name() string { return "Grep" }
func (t *GrepTool) Description() string {
	return "Search files by regex pattern."
}
func (t *GrepTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern":     map[string]any{"type": "string"},
					"path":        map[string]any{"type": "string"},
					"glob":        map[string]any{"type": "string"},
					"output_mode": map[string]any{"type": "string"},
					"multiline":   map[string]any{"type": "boolean"},
					"-i":          map[string]any{"type": "boolean"},
					"-A":          map[string]any{"type": "integer"},
					"-B":          map[string]any{"type": "integer"},
					"-C":          map[string]any{"type": "integer"},
					"head_limit":  map[string]any{"type": "integer"},
					"offset":      map[string]any{"type": "integer"},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

func (t *GrepTool) Execute(ctx context.Context, params map[string]any) Result {
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

	caseInsensitive := asBool(params["-i"])
	multiline := asBool(params["multiline"])
	mode := "content"
	if m, ok := params["output_mode"].(string); ok && strings.TrimSpace(m) != "" {
		mode = m
	}
	headLimit := asInt(params["head_limit"], 5000)
	offset := asInt(params["offset"], 0)
	globPattern, _ := params["glob"].(string)
	before := asInt(params["-B"], 0)
	after := asInt(params["-A"], 0)
	contextLines := asInt(params["-C"], 0)
	if contextLines > 0 {
		before, after = contextLines, contextLines
	}

	pat := pattern
	if caseInsensitive {
		pat = "(?i)" + pat
	}
	if multiline {
		pat = "(?s)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return errResult(fmt.Sprintf("invalid regex: %v", err))
	}

	files := make([]string, 0, 256)
	if err := filepath.WalkDir(basePath, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || d.IsDir() {
			if err == nil && d != nil && d.IsDir() && path != basePath && isIgnoredSearchDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if globPattern != "" {
			ok, gErr := filepath.Match(globPattern, filepath.Base(path))
			if gErr != nil || !ok {
				return nil
			}
		}
		files = append(files, path)
		return nil
	}); err != nil {
		if ctx.Err() != nil {
			return errResult(ctx.Err().Error())
		}
		return errResult(fmt.Sprintf("walk path: %v", err))
	}
	sort.Strings(files)

	matches := make([]string, 0, 1024)
	fileHits := map[string]int{}
	outputGoal := offset + headLimit
	if headLimit <= 0 {
		outputGoal = 0
	}
	for _, file := range files {
		if ctx.Err() != nil {
			return errResult(ctx.Err().Error())
		}
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		if isBinary(data) {
			continue
		}
		content := string(data)
		switch mode {
		case "files_with_matches":
			if re.MatchString(content) {
				fileHits[file]++
				if outputGoal > 0 && len(fileHits) >= outputGoal {
					goto doneScanning
				}
			}
		case "count":
			found := re.FindAllStringIndex(content, -1)
			if len(found) > 0 {
				fileHits[file] = len(found)
			}
		default:
			lines := splitLines(content)
			for i, line := range lines {
				if !re.MatchString(line) {
					continue
				}
				fileHits[file]++
				start := i - before
				if start < 0 {
					start = 0
				}
				end := i + after
				if end >= len(lines) {
					end = len(lines) - 1
				}
				for j := start; j <= end; j++ {
					matches = append(matches, fmt.Sprintf("%s:%d:%s", file, j+1, lines[j]))
				}
				if outputGoal > 0 && len(matches) >= outputGoal {
					goto doneScanning
				}
			}
		}
	}

doneScanning:
	orderedFiles := make([]string, 0, len(fileHits))
	for f := range fileHits {
		orderedFiles = append(orderedFiles, f)
	}
	sort.Strings(orderedFiles)

	switch mode {
	case "files_with_matches":
		return Result{Output: paginate(strings.Join(orderedFiles, "\n"), offset, headLimit)}
	case "count":
		lines := make([]string, 0, len(orderedFiles))
		for _, f := range orderedFiles {
			lines = append(lines, f+":"+strconv.Itoa(fileHits[f]))
		}
		return Result{Output: paginate(strings.Join(lines, "\n"), offset, headLimit)}
	default:
		return Result{Output: paginate(strings.Join(matches, "\n"), offset, headLimit)}
	}
}

func paginate(content string, offset, limit int) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	lines := splitLines(content)
	if offset < 0 {
		offset = 0
	}
	if offset >= len(lines) {
		return ""
	}
	lines = lines[offset:]
	if limit > 0 && len(lines) > limit {
		lines = lines[:limit]
	}
	return strings.Join(lines, "\n")
}

func splitLines(s string) []string {
	scanner := bufio.NewScanner(strings.NewReader(s))
	out := make([]string, 0, 64)
	for scanner.Scan() {
		out = append(out, scanner.Text())
	}
	return out
}

func isBinary(data []byte) bool {
	max := 512
	if len(data) < max {
		max = len(data)
	}
	for i := 0; i < max; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

func asBool(v any) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return strings.EqualFold(val, "true")
	default:
		return false
	}
}

func asInt(v any, fallback int) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		if parsed, err := strconv.Atoi(val); err == nil {
			return parsed
		}
	}
	return fallback
}
